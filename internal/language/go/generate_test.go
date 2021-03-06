/* Copyright 2018 The Bazel Authors. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package golang

import (
	"io/ioutil"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bazelbuild/bazel-gazelle/internal/config"
	"github.com/bazelbuild/bazel-gazelle/internal/language/proto"
	"github.com/bazelbuild/bazel-gazelle/internal/merger"
	"github.com/bazelbuild/bazel-gazelle/internal/rule"
	"github.com/bazelbuild/bazel-gazelle/internal/walk"
	bzl "github.com/bazelbuild/buildtools/build"
)

func TestGenerateRules(t *testing.T) {
	c, _, langs := testConfig()
	c.RepoRoot = "testdata"
	c.Dirs = []string{c.RepoRoot}
	c.ValidBuildFileNames = []string{"BUILD.old"}
	gc := getGoConfig(c)
	gc.prefix = "example.com/repo"

	cexts := make([]config.Configurer, len(langs))
	var loads []rule.LoadInfo
	for i, lang := range langs {
		cexts[i] = lang
		loads = append(loads, lang.Loads()...)
	}
	walk.Walk(c, cexts, func(dir, rel string, c *config.Config, update bool, oldFile *rule.File, subdirs, regularFiles, genFiles []string) {
		t.Run(rel, func(t *testing.T) {
			var empty, gen []*rule.Rule
			for _, lang := range langs {
				e, g := lang.GenerateRules(c, dir, rel, oldFile, subdirs, regularFiles, genFiles, gen)
				empty = append(empty, e...)
				gen = append(gen, g...)
			}
			isTest := false
			for _, name := range regularFiles {
				if name == "BUILD.want" {
					isTest = true
					break
				}
			}
			if !isTest {
				// GenerateRules may have side effects, so we need to run it, even if
				// there's no test.
				return
			}
			f := rule.EmptyFile("test")
			for _, r := range gen {
				r.Insert(f)
			}
			convertImportsAttrs(f)
			merger.FixLoads(f, loads)
			f.Sync()
			got := string(bzl.Format(f.File))
			wantPath := filepath.Join(dir, "BUILD.want")
			wantBytes, err := ioutil.ReadFile(wantPath)
			if err != nil {
				t.Fatalf("error reading %s: %v", wantPath, err)
			}
			want := string(wantBytes)

			if got != want {
				t.Errorf("GenerateRules %q: got:\n%s\nwant:\n%s", rel, got, want)
			}
		})
	})
}

func TestGenerateRulesEmpty(t *testing.T) {
	c, _, langs := testConfig()
	goLang := langs[1].(*goLang)
	empty, gen := goLang.GenerateRules(c, "./foo", "foo", nil, nil, nil, nil, nil)
	if len(gen) > 0 {
		t.Errorf("got %d generated rules; want 0", len(gen))
	}
	f := rule.EmptyFile("test")
	for _, r := range empty {
		r.Insert(f)
	}
	f.Sync()
	got := strings.TrimSpace(string(bzl.Format(f.File)))
	want := strings.TrimSpace(`
filegroup(name = "go_default_library_protos")

go_proto_library(name = "foo_go_proto")

go_library(name = "go_default_library")

go_binary(name = "foo")

go_test(name = "go_default_test")
`)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestGeneratorEmptyLegacyProto(t *testing.T) {
	c, _, langs := testConfig()
	goLang := langs[1].(*goLang)
	pc := proto.GetProtoConfig(c)
	pc.Mode = proto.LegacyMode
	empty, _ := goLang.GenerateRules(c, "./foo", "foo", nil, nil, nil, nil, nil)
	for _, e := range empty {
		if kind := e.Kind(); kind == "proto_library" || kind == "go_proto_library" || kind == "go_grpc_library" {
			t.Errorf("deleted rule %s ; should not delete in legacy proto mode", kind)
		}
	}
}

// convertImportsAttrs copies private attributes to regular attributes, which
// will later be written out to build files. This allows tests to check the
// values of private attributes with simple string comparison.
func convertImportsAttrs(f *rule.File) {
	for _, r := range f.Rules {
		v := r.PrivateAttr(config.GazelleImportsKey)
		if v != nil {
			r.SetAttr(config.GazelleImportsKey, v)
		}
	}
}
