// User-owned LawSpec adapter.
package policy

// WorkspaceHandler is the native handler of Workspace: writeFile, revoke, restore.
type WorkspaceHandler struct{}

// NewWorkspaceHandler makes the native handler the generated tests use.
func NewWorkspaceHandler() Workspace {
	return &WorkspaceHandler{}
}

func (handler *WorkspaceHandler) WriteFile(value0 string, value1 []byte) {
	panic("writeFile")
}

func (handler *WorkspaceHandler) Revoke(value0 string) {
	panic("revoke")
}

func (handler *WorkspaceHandler) Restore(value0 string) {
	panic("restore")
}
