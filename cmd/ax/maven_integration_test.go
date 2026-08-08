//go:build maven_integration

package main

import (
	"context"
	"strings"
	"testing"

	"goforge.dev/assayxport/internal/artifact/maven"
	"goforge.dev/assayxport/internal/emit"
)

func TestPinnedMavenArtifacts(t *testing.T) {
	tests := []struct{ coord, landmark string }{
		{"org.slf4j:slf4j-api:2.0.17", "Logger"},
		{"org.apache.commons:commons-lang3:3.17.0", "StringUtils"},
		{"io.vavr:vavr:0.10.6", "Tuple"},
		{"com.google.guava:guava:33.4.0-jre", "ImmutableList"},
		{"com.fasterxml.jackson.core:jackson-databind:2.18.2", "ObjectMapper"},
	}
	for _, tt := range tests {
		t.Run(tt.coord, func(t *testing.T) {
			c, e := maven.Parse(tt.coord)
			if e != nil {
				t.Fatal(e)
			}
			r := maven.Resolver{}
			a, e := r.Resolve(context.Background(), c)
			if e != nil {
				t.Fatal(e)
			}
			idx, shards, e := assayJAR(a.Path, "mvn:"+c.String(), 25)
			if e != nil {
				t.Fatal(e)
			}
			one, e := emit.Combined(idx, shards)
			if e != nil {
				t.Fatal(e)
			}
			idx2, shards2, e := assayJAR(a.Path, "local.jar", 25)
			if e != nil {
				t.Fatal(e)
			}
			two, e := emit.Combined(idx2, shards2)
			if e != nil {
				t.Fatal(e)
			}
			if string(one) != string(two) {
				t.Fatal("coordinate and local scans differ")
			}
			if !strings.Contains(string(one), `"name": "`+tt.landmark+`"`) {
				t.Fatalf("missing landmark %s", tt.landmark)
			}
		})
	}
}
