package publication

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const CentralPortal = "https://central.sonatype.com"
type Deployment struct{DeploymentID string `json:"deploymentId"`;DeploymentName string `json:"deploymentName"`;DeploymentState string `json:"deploymentState"`;PURLs []string `json:"purls"`;Errors any `json:"errors"`}
type releaseState struct{Version string `json:"version"`;BundleSHA256 string `json:"bundle_sha256"`;DeploymentID string `json:"deployment_id"`;State string `json:"state"`}
type CentralClient struct{BaseURL,Username,Password string;HTTP *http.Client}

// VerifyGoRelease proves the immutable Go half of a dual publication before
// any credential is read or upload is attempted.
func VerifyGoRelease(ctx context.Context, root string, cfg Config) error {
	if cfg.Policy != "goplus-dual" { return nil }
	tag := "v"+cfg.Version
	head := gitOutput(root,"rev-parse","HEAD")
	cmd := exec.CommandContext(ctx,"git","-C",root,"ls-remote","--tags","origin","refs/tags/"+tag,"refs/tags/"+tag+"^{}")
	out,err:=cmd.CombinedOutput();if err!=nil{return fmt.Errorf("[REMOTE_TAG_UNAVAILABLE] cannot read %s from origin: %v: %s\n  Fix: verify the origin remote and network, then push the reviewed tag with `git push origin %s` before retrying",tag,err,strings.TrimSpace(string(out)),tag)}
	remote:="";for _,line:=range strings.Split(strings.TrimSpace(string(out)),"\n"){fields:=strings.Fields(line);if len(fields)==2{remote=fields[0];if strings.HasSuffix(fields[1],"^{}"){break}}}
	if remote==""{return fmt.Errorf("[REMOTE_TAG_MISSING] origin does not contain %s\n  Fix: push the approved immutable Go release first with `git push origin %s`, wait for it to become visible, then rerun `ax publish`",tag,tag)}
	if remote!=head{return fmt.Errorf("[REMOTE_TAG_COMMIT_MISMATCH] origin %s points to %s but the release checkout is %s\n  Fix: stop publication and reconcile the tag; Maven coordinates must be built from the exact remote Go tag commit",tag,remote,head)}
	proxy:=strings.TrimRight(strings.TrimSpace(os.Getenv("AX_GO_PROXY_URL")),"/");if proxy==""{proxy="https://proxy.golang.org"}
	versionURL:=proxy+"/"+proxyEscape(cfg.GoModule)+"/@v/"+tag+".info"
	if err:=requireHTTP(ctx,versionURL,"GO_PROXY_VERSION_MISSING","Publish "+tag+" first, then wait for proxy.golang.org to cache "+cfg.GoModule+"@"+tag+" before retrying.");err!=nil{return err}
	pkgSite:=strings.TrimRight(strings.TrimSpace(os.Getenv("AX_PKG_GO_DEV_URL")),"/");if pkgSite==""{pkgSite="https://pkg.go.dev"}
	if err:=requireHTTP(ctx,pkgSite+"/"+cfg.GoModule+"@"+tag,"PKG_GO_DEV_NOT_INDEXED","Request/index "+cfg.GoModule+"@"+tag+" on pkg.go.dev and retry only after its documentation page returns successfully.");err!=nil{return err}
	return nil
}

func requireHTTP(ctx context.Context, endpoint, code, fix string)error{req,err:=http.NewRequestWithContext(ctx,http.MethodGet,endpoint,nil);if err!=nil{return err};resp,err:=http.DefaultClient.Do(req);if err!=nil{return fmt.Errorf("[%s] %s: %v\n  Fix: %s",code,endpoint,err,fix)};defer resp.Body.Close();if resp.StatusCode!=http.StatusOK{io.Copy(io.Discard,io.LimitReader(resp.Body,1<<20));return fmt.Errorf("[%s] %s returned HTTP %d\n  Fix: %s",code,endpoint,resp.StatusCode,fix)};return nil}
func proxyEscape(value string)string{var b strings.Builder;for _,r:=range value{if r>='A'&&r<='Z'{b.WriteByte('!');b.WriteRune(r-'A'+'a')}else{b.WriteRune(r)}};return b.String()}

// Publish uploads USER_MANAGED, waits for validation, explicitly promotes, and
// waits for publication. Non-secret state makes interrupted runs resumable.
func Publish(ctx context.Context,root string,cfg Config,prepared Prepared)(Deployment,error){
	username,password:=strings.TrimSpace(os.Getenv("MAVEN_CENTRAL_USERNAME")),strings.TrimSpace(os.Getenv("MAVEN_CENTRAL_PASSWORD"));if username==""||password==""{return Deployment{},fmt.Errorf("[CENTRAL_CREDENTIALS_MISSING] Maven Central credentials are absent\n  Fix: export MAVEN_CENTRAL_USERNAME and MAVEN_CENTRAL_PASSWORD using a Central Portal user token, then rerun `ax publish`")}
	bundle,err:=os.ReadFile(prepared.Bundle);if err!=nil{return Deployment{},err};sum:=sha256.Sum256(bundle);digest:=hex.EncodeToString(sum[:])
	statePath:=filepath.Join(root,".assayxport","releases",cfg.Version+".json");state,err:=readReleaseState(statePath);if err!=nil{return Deployment{},err};if state.BundleSHA256!=""&&state.BundleSHA256!=digest{return Deployment{},fmt.Errorf("[RELEASE_STATE_CONFLICT] saved deployment uses a different bundle digest\n  Fix: restore the exact prepared bundle or intentionally move %s aside before starting a new deployment",statePath)}
	client:=CentralClient{BaseURL:strings.TrimSpace(os.Getenv("AX_MAVEN_CENTRAL_URL")),Username:username,Password:password};id:=state.DeploymentID
	if id==""{if len(prepared.PublicKey)>0{if err=publishPublicKey(ctx,prepared.PublicKey);err!=nil{return Deployment{},err}};id,err=client.Upload(ctx,prepared.Bundle,cfg.GroupID+":"+cfg.ArtifactID+":"+cfg.Version);if err!=nil{return Deployment{},err};state=releaseState{cfg.Version,digest,id,"UPLOADED"};if err=writeReleaseState(statePath,state);err!=nil{return Deployment{},err}}
	deployment,err:=client.Wait(ctx,id,"VALIDATED");if err!=nil{return deployment,err};state.State=deployment.DeploymentState;_ = writeReleaseState(statePath,state)
	if deployment.DeploymentState=="VALIDATED"{if err=client.Promote(ctx,id);err!=nil{return deployment,err}}
	deployment,err=client.Wait(ctx,id,"PUBLISHED");if err!=nil{return deployment,err};state.State=deployment.DeploymentState;if err=writeReleaseState(statePath,state);err!=nil{return deployment,err};return deployment,nil
}
func(c CentralClient)Upload(ctx context.Context,bundle,name string)(string,error){data,err:=os.ReadFile(bundle);if err!=nil{return "",err};var body bytes.Buffer;w:=multipart.NewWriter(&body);part,err:=w.CreateFormFile("bundle",filepath.Base(bundle));if err!=nil{return "",err};if _,err=part.Write(data);err!=nil{return "",err};if err=w.Close();err!=nil{return "",err};endpoint:=c.base()+"/api/v1/publisher/upload?publishingType=USER_MANAGED&name="+url.QueryEscape(name);req,err:=http.NewRequestWithContext(ctx,http.MethodPost,endpoint,&body);if err!=nil{return "",err};req.Header.Set("Content-Type",w.FormDataContentType());c.auth(req);resp,err:=c.client().Do(req);if err!=nil{return "",err};defer resp.Body.Close();out,_:=io.ReadAll(io.LimitReader(resp.Body,1<<20));if resp.StatusCode!=http.StatusCreated{return "",fmt.Errorf("Central upload: HTTP %d: %s",resp.StatusCode,strings.TrimSpace(string(out)))};return strings.TrimSpace(string(out)),nil}
func(c CentralClient)Status(ctx context.Context,id string)(Deployment,error){req,err:=http.NewRequestWithContext(ctx,http.MethodPost,c.base()+"/api/v1/publisher/status?id="+url.QueryEscape(id),nil);if err!=nil{return Deployment{},err};c.auth(req);resp,err:=c.client().Do(req);if err!=nil{return Deployment{},err};defer resp.Body.Close();if resp.StatusCode!=http.StatusOK{body,_:=io.ReadAll(io.LimitReader(resp.Body,1<<20));return Deployment{},fmt.Errorf("Central status: HTTP %d: %s",resp.StatusCode,strings.TrimSpace(string(body)))};var d Deployment;if err=json.NewDecoder(resp.Body).Decode(&d);err!=nil{return d,err};return d,nil}
func(c CentralClient)Promote(ctx context.Context,id string)error{req,err:=http.NewRequestWithContext(ctx,http.MethodPost,c.base()+"/api/v1/publisher/deployment/"+url.PathEscape(id),nil);if err!=nil{return err};c.auth(req);resp,err:=c.client().Do(req);if err!=nil{return err};defer resp.Body.Close();if resp.StatusCode!=http.StatusNoContent{body,_:=io.ReadAll(io.LimitReader(resp.Body,1<<20));return fmt.Errorf("Central promotion: HTTP %d: %s",resp.StatusCode,strings.TrimSpace(string(body)))};return nil}
func(c CentralClient)Wait(ctx context.Context,id,target string)(Deployment,error){for{d,err:=c.Status(ctx,id);if err!=nil{return d,err};if d.DeploymentState==target||target=="PUBLISHED"&&d.DeploymentState=="PUBLISHED"{return d,nil};if d.DeploymentState=="FAILED"{return d,fmt.Errorf("Central deployment failed: %v",d.Errors)};select{case<-ctx.Done():return d,ctx.Err();case<-time.After(2*time.Second):}}}
func(c CentralClient)base()string{if c.BaseURL!=""{return strings.TrimRight(c.BaseURL,"/")};return CentralPortal};func(c CentralClient)client()*http.Client{if c.HTTP!=nil{return c.HTTP};return &http.Client{Timeout:2*time.Minute}};func(c CentralClient)auth(req *http.Request){token:=base64.StdEncoding.EncodeToString([]byte(c.Username+":"+c.Password));req.Header.Set("Authorization","Bearer "+token)}
func readReleaseState(path string)(releaseState,error){data,err:=os.ReadFile(path);if os.IsNotExist(err){return releaseState{},nil};if err!=nil{return releaseState{},err};var state releaseState;if err=json.Unmarshal(data,&state);err!=nil{return state,fmt.Errorf("invalid release state %s: %w",path,err)};return state,nil}
func writeReleaseState(path string,state releaseState)error{data,err:=json.MarshalIndent(state,"","  ");if err!=nil{return err};data=append(data,'\n');if err=os.MkdirAll(filepath.Dir(path),0o755);err!=nil{return err};return os.WriteFile(path,data,0o600)}
func publishPublicKey(ctx context.Context,key []byte)error{base:=strings.TrimSpace(os.Getenv("AX_MAVEN_KEYSERVER_URL"));if base==""{base="https://keyserver.ubuntu.com"};form:=url.Values{"keytext":{string(key)}};req,err:=http.NewRequestWithContext(ctx,http.MethodPost,strings.TrimRight(base,"/")+"/pks/add",strings.NewReader(form.Encode()));if err!=nil{return err};req.Header.Set("Content-Type","application/x-www-form-urlencoded");resp,err:=http.DefaultClient.Do(req);if err!=nil{return fmt.Errorf("publishing OpenPGP key: %w",err)};defer resp.Body.Close();if resp.StatusCode>=400&&resp.StatusCode!=http.StatusConflict{body,_:=io.ReadAll(io.LimitReader(resp.Body,1<<20));return fmt.Errorf("publishing OpenPGP key: HTTP %d: %s",resp.StatusCode,strings.TrimSpace(string(body)))};return nil}
