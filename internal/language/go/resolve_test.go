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
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bazelbuild/bazel-gazelle/internal/config"
	"github.com/bazelbuild/bazel-gazelle/internal/label"
	"github.com/bazelbuild/bazel-gazelle/internal/repos"
	"github.com/bazelbuild/bazel-gazelle/internal/resolve"
	"github.com/bazelbuild/bazel-gazelle/internal/rule"
	bzl "github.com/bazelbuild/buildtools/build"
	"golang.org/x/tools/go/vcs"
)

func TestResolveGo(t *testing.T) {
	type buildFile struct {
		rel, content string
	}
	type testCase struct {
		desc  string
		index []buildFile
		old   buildFile
		want  string
	}
	for _, tc := range []testCase{
		{
			desc: "std",
			index: []buildFile{{
				rel: "bad",
				content: `
go_library(
    name = "go_default_library",
    importpath = "fmt",
)
`,
			}},
			old: buildFile{
				content: `
go_binary(
    name = "dep",
    _imports = ["fmt"],
)
`,
			},
			want: `go_binary(name = "dep")`,
		}, {
			desc: "self_import",
			old: buildFile{content: `
go_library(
    name = "go_default_library",
    importpath = "foo",
    _imports = ["foo"],
)
`},
			want: `
go_library(
    name = "go_default_library",
    importpath = "foo",
)
`,
		}, {
			desc: "same_package",
			old: buildFile{content: `
go_library(
    name = "a",
    importpath = "foo",
)

go_binary(
    name = "b",
    _imports = ["foo"],
)
`},
			want: `
go_library(
    name = "a",
    importpath = "foo",
)

go_binary(
    name = "b",
    deps = [":a"],
)
`,
		}, {
			desc: "different_package",
			index: []buildFile{{
				rel: "a",
				content: `
go_library(
    name = "a_lib",
    importpath = "aa",
)
`,
			}},
			old: buildFile{
				rel: "b",
				content: `
go_binary(
    name = "bin",
    _imports = ["aa"],
)
`,
			},
			want: `
go_binary(
    name = "bin",
    deps = ["//a:a_lib"],
)
`,
		}, {
			desc: "multiple_rules_ambiguous",
			index: []buildFile{{
				rel: "foo",
				content: `
go_library(
    name = "a",
    importpath = "example.com/foo",
)

go_library(
    name = "b",
    importpath = "example.com/foo",
)
`,
			}},
			old: buildFile{content: `
go_binary(
    name = "bin",
    _imports = ["example.com/foo"],
)
`,
			},
			// an error should be reported, and no dependency should be emitted
			want: `go_binary(name = "bin")`,
		}, {
			desc: "vendor_not_visible",
			index: []buildFile{
				{
					rel: "",
					content: `
go_library(
    name = "root",
    importpath = "example.com/foo",
)
`,
				}, {
					rel: "a/vendor/foo",
					content: `
go_library(
    name = "vendored",
    importpath = "example.com/foo",
)
`,
				},
			},
			old: buildFile{
				rel: "b",
				content: `
go_binary(
    name = "bin",
    _imports = ["example.com/foo"],
)
`,
			},
			want: `
go_binary(
    name = "bin",
    deps = ["//:root"],
)
`,
		}, {
			desc: "vendor_supercedes_nonvendor",
			index: []buildFile{
				{
					rel: "",
					content: `
go_library(
    name = "root",
    importpath = "example.com/foo",
)
`,
				}, {
					rel: "vendor/foo",
					content: `
go_library(
    name = "vendored",
    importpath = "example.com/foo",
)
`,
				},
			},
			old: buildFile{
				rel: "sub",
				content: `
go_binary(
    name = "bin",
    _imports = ["example.com/foo"],
)
`,
			},
			want: `
go_binary(
    name = "bin",
    deps = ["//vendor/foo:vendored"],
)
`,
		}, {
			desc: "deep_vendor_shallow_vendor",
			index: []buildFile{
				{
					rel: "shallow/vendor",
					content: `
go_library(
    name = "shallow",
    importpath = "example.com/foo",
)
`,
				}, {
					rel: "shallow/deep/vendor",
					content: `
go_library(
    name = "deep",
    importpath = "example.com/foo",
)
`,
				},
			},
			old: buildFile{
				rel: "shallow/deep",
				content: `
go_binary(
    name = "bin",
    _imports = ["example.com/foo"],
)
`,
			},
			want: `
go_binary(
    name = "bin",
    deps = ["//shallow/deep/vendor:deep"],
)
`,
		}, {
			desc: "nested_vendor",
			index: []buildFile{
				{
					rel: "vendor/a",
					content: `
go_library(
    name = "a",
    importpath = "a",
)
`,
				}, {
					rel: "vendor/b/vendor/a",
					content: `
go_library(
    name = "a",
    importpath = "a",
)
`,
				},
			},
			old: buildFile{
				rel: "vendor/b/c",
				content: `
go_binary(
    name = "bin",
    _imports = ["a"],
)
`,
			},
			want: `
go_binary(
    name = "bin",
    deps = ["//vendor/b/vendor/a"],
)
`,
		}, {
			desc: "skip_self_embed",
			old: buildFile{content: `
go_library(
    name = "go_default_library",
    srcs = ["lib.go"],
    importpath = "example.com/repo/lib",
)

go_test(
    name = "go_default_test",
    embed = [":go_default_library"],
    _imports = ["example.com/repo/lib"],
)
`,
			},
			want: `
go_library(
    name = "go_default_library",
    srcs = ["lib.go"],
    importpath = "example.com/repo/lib",
)

go_test(
    name = "go_default_test",
    embed = [":go_default_library"],
)
`,
		}, {
			desc: "binary_embed",
			old: buildFile{content: `
go_library(
    name = "a",
    importpath = "a",
)

go_library(
    name = "b",
    embed = [":a"],
)

go_binary(
    name = "c",
    embed = [":a"],
    importpath = "a",
)

go_library(
    name = "d",
    _imports = ["a"],
)
`},
			want: `
go_library(
    name = "a",
    importpath = "a",
)

go_library(
    name = "b",
    embed = [":a"],
)

go_binary(
    name = "c",
    embed = [":a"],
    importpath = "a",
)

go_library(
    name = "d",
    deps = [":b"],
)
`,
		}, {
			desc: "local_unknown",
			old: buildFile{content: `
go_binary(
    name = "bin",
    _imports = [
        "example.com/repo/resolve",
        "example.com/repo/resolve/sub",
    ],
)
`},
			want: `
go_binary(
    name = "bin",
    deps = [
        ":go_default_library",
        "//sub:go_default_library",
    ],
)
`,
		}, {
			desc: "local_relative",
			old: buildFile{
				rel: "a",
				content: `
go_binary(
    name = "bin",
    _imports = [
        ".",
        "./b",
        "../c",
    ],
)
`,
			},
			want: `
go_binary(
    name = "bin",
    deps = [
        ":go_default_library",
        "//a/b:go_default_library",
        "//c:go_default_library",
    ],
)
`,
		}, {
			desc: "vendor_no_index",
			old: buildFile{content: `
go_binary(
    name = "bin",
    _imports = ["example.com/outside/prefix"],
)
`},
			want: `
go_binary(
    name = "bin",
    deps = ["//vendor/example.com/outside/prefix:go_default_library"],
)
`,
		}, {
			desc: "test_and_library_not_indexed",
			index: []buildFile{{
				rel: "foo",
				content: `
go_test(
    name = "go_default_test",
    importpath = "example.com/foo",
)

go_binary(
    name = "cmd",
    importpath = "example.com/foo",
)
`,
			}},
			old: buildFile{content: `
go_binary(
    name = "bin",
    _imports = ["example.com/foo"],
)
`},
			want: `
go_binary(
    name = "bin",
    deps = ["//vendor/example.com/foo:go_default_library"],
)`,
		}, {
			desc: "proto_index",
			index: []buildFile{{
				rel: "sub",
				content: `
proto_library(
    name = "foo_proto",
    srcs = ["bar.proto"],
)

go_proto_library(
    name = "foo_go_proto",
    importpath = "example.com/foo",
    proto = ":foo_proto",
)

go_library(
    name = "embed",
    embed = [":foo_go_proto"],
    importpath = "example.com/foo",
)
`,
			}},
			old: buildFile{content: `
go_proto_library(
    name = "dep_proto",
    _imports = ["sub/bar.proto"],
)
`},
			want: `
go_proto_library(
    name = "dep_proto",
    deps = ["//sub:embed"],
)
`,
		}, {
			desc: "proto_embed",
			old: buildFile{content: `
proto_library(
    name = "foo_proto",
    srcs = ["foo.proto"],
)

go_proto_library(
    name = "foo_go_proto",
    importpath = "example.com/repo/foo",
    proto = ":foo_proto",
)

go_library(
    name = "foo_embedder",
    embed = [":foo_go_proto"],
)

proto_library(
    name = "bar_proto",
    srcs = ["bar.proto"],
    _imports = ["foo.proto"],
)

go_proto_library(
    name = "bar_go_proto",
    proto = ":bar_proto",
    _imports = ["foo.proto"],
)
`},
			want: `
proto_library(
    name = "foo_proto",
    srcs = ["foo.proto"],
)

go_proto_library(
    name = "foo_go_proto",
    importpath = "example.com/repo/foo",
    proto = ":foo_proto",
)

go_library(
    name = "foo_embedder",
    embed = [":foo_go_proto"],
)

proto_library(
    name = "bar_proto",
    srcs = ["bar.proto"],
    deps = [":foo_proto"],
)

go_proto_library(
    name = "bar_go_proto",
    proto = ":bar_proto",
    deps = [":foo_embedder"],
)
`,
		}, {
			desc: "proto_wkt",
			old: buildFile{content: `
go_proto_library(
    name = "wkts_go_proto",
    _imports = [
        "google/protobuf/any.proto",
        "google/protobuf/api.proto",
        "google/protobuf/compiler_plugin.proto",
        "google/protobuf/descriptor.proto",
        "google/protobuf/duration.proto",
        "google/protobuf/empty.proto",
        "google/protobuf/field_mask.proto",
        "google/protobuf/source_context.proto",
        "google/protobuf/struct.proto",
        "google/protobuf/timestamp.proto",
        "google/protobuf/type.proto",
        "google/protobuf/wrappers.proto",
   ],
)

go_library(
    name = "wkts_go_lib",
    _imports = [
        "github.com/golang/protobuf/ptypes/any",
        "github.com/golang/protobuf/ptypes/api",
        "github.com/golang/protobuf/protoc-gen-go/descriptor",
        "github.com/golang/protobuf/ptypes/duration",
        "github.com/golang/protobuf/ptypes/empty",
        "google.golang.org/genproto/protobuf/field_mask",
        "google.golang.org/genproto/protobuf/source_context",
        "github.com/golang/protobuf/ptypes/struct",
        "github.com/golang/protobuf/ptypes/timestamp",
        "github.com/golang/protobuf/ptypes/wrappers",
        "github.com/golang/protobuf/protoc-gen-go/plugin",
        "google.golang.org/genproto/protobuf/ptype",
   ],
)
`},
			want: `
go_proto_library(name = "wkts_go_proto")

go_library(
    name = "wkts_go_lib",
    deps = [
        "@io_bazel_rules_go//proto/wkt:any_go_proto",
        "@io_bazel_rules_go//proto/wkt:api_go_proto",
        "@io_bazel_rules_go//proto/wkt:compiler_plugin_go_proto",
        "@io_bazel_rules_go//proto/wkt:descriptor_go_proto",
        "@io_bazel_rules_go//proto/wkt:duration_go_proto",
        "@io_bazel_rules_go//proto/wkt:empty_go_proto",
        "@io_bazel_rules_go//proto/wkt:field_mask_go_proto",
        "@io_bazel_rules_go//proto/wkt:source_context_go_proto",
        "@io_bazel_rules_go//proto/wkt:struct_go_proto",
        "@io_bazel_rules_go//proto/wkt:timestamp_go_proto",
        "@io_bazel_rules_go//proto/wkt:type_go_proto",
        "@io_bazel_rules_go//proto/wkt:wrappers_go_proto",
    ],
)
`,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			c, _, langs := testConfig()
			gc := getGoConfig(c)
			gc.prefix = "example.com/repo/resolve"
			gc.depMode = vendorMode
			kindToResolver := make(map[string]resolve.Resolver)
			for _, lang := range langs {
				for kind := range lang.Kinds() {
					kindToResolver[kind] = lang
				}
			}
			ix := resolve.NewRuleIndex(kindToResolver)
			rc := testRemoteCache(nil)

			for _, bf := range tc.index {
				buildPath := filepath.Join(filepath.FromSlash(bf.rel), "BUILD.bazel")
				f, err := rule.LoadData(buildPath, []byte(bf.content))
				if err != nil {
					t.Fatal(err)
				}
				for _, r := range f.Rules {
					ix.AddRule(c, r, f)
				}
			}
			buildPath := filepath.Join(filepath.FromSlash(tc.old.rel), "BUILD.bazel")
			f, err := rule.LoadData(buildPath, []byte(tc.old.content))
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range f.Rules {
				convertImportsAttr(r)
				ix.AddRule(c, r, f)
			}
			ix.Finish()
			for _, r := range f.Rules {
				kindToResolver[r.Kind()].Resolve(c, ix, rc, r, label.New("", tc.old.rel, r.Name()))
			}
			f.Sync()
			got := strings.TrimSpace(string(bzl.Format(f.File)))
			want := strings.TrimSpace(tc.want)
			if got != want {
				t.Errorf("got:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

func TestResolveExternal(t *testing.T) {
	c, _, langs := testConfig()
	gc := getGoConfig(c)
	gc.prefix = "example.com/local"
	ix := resolve.NewRuleIndex(nil)
	ix.Finish()
	gl := langs[1].(*goLang)
	for _, tc := range []struct {
		desc, importpath string
		repos            []repos.Repo
		want             string
	}{
		{
			desc:       "top",
			importpath: "example.com/repo",
			want:       "@com_example_repo//:go_default_library",
		}, {
			desc:       "sub",
			importpath: "example.com/repo/lib",
			want:       "@com_example_repo//lib:go_default_library",
		}, {
			desc: "custom_repo",
			repos: []repos.Repo{{
				Name:     "custom_repo_name",
				GoPrefix: "example.com/repo",
			}},
			importpath: "example.com/repo/lib",
			want:       "@custom_repo_name//lib:go_default_library",
		}, {
			desc:       "qualified",
			importpath: "example.com/repo.git/lib",
			want:       "@com_example_repo_git//lib:go_default_library",
		}, {
			desc:       "domain",
			importpath: "example.com/lib",
			want:       "@com_example//lib:go_default_library",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			rc := testRemoteCache(tc.repos)
			r := rule.NewRule("go_library", "x")
			imports := rule.PlatformStrings{Generic: []string{tc.importpath}}
			r.SetPrivateAttr(config.GazelleImportsKey, imports)
			gl.Resolve(c, ix, rc, r, label.New("", "", "x"))
			deps := r.AttrStrings("deps")
			if len(deps) != 1 {
				t.Fatalf("deps: got %d; want 1", len(deps))
			}
			if deps[0] != tc.want {
				t.Errorf("got %s; want %s", deps[0], tc.want)
			}
		})
	}
}

func testRemoteCache(knownRepos []repos.Repo) *repos.RemoteCache {
	rc := repos.NewRemoteCache(knownRepos)
	rc.RepoRootForImportPath = stubRepoRootForImportPath
	rc.HeadCmd = func(remote, vcs string) (string, error) {
		return "", fmt.Errorf("HeadCmd not supported in test")
	}
	return rc
}

// stubRepoRootForImportPath is a stub implementation of vcs.RepoRootForImportPath
func stubRepoRootForImportPath(importpath string, verbose bool) (*vcs.RepoRoot, error) {
	if strings.HasPrefix(importpath, "example.com/repo.git") {
		return &vcs.RepoRoot{
			VCS:  vcs.ByCmd("git"),
			Repo: "https://example.com/repo.git",
			Root: "example.com/repo.git",
		}, nil
	}

	if strings.HasPrefix(importpath, "example.com/repo") {
		return &vcs.RepoRoot{
			VCS:  vcs.ByCmd("git"),
			Repo: "https://example.com/repo.git",
			Root: "example.com/repo",
		}, nil
	}

	if strings.HasPrefix(importpath, "example.com") {
		return &vcs.RepoRoot{
			VCS:  vcs.ByCmd("git"),
			Repo: "https://example.com",
			Root: "example.com",
		}, nil
	}

	return nil, fmt.Errorf("could not resolve import path: %q", importpath)
}

func convertImportsAttr(r *rule.Rule) {
	kind := r.Kind()
	value := r.AttrStrings("_imports")
	r.DelAttr("_imports")
	if _, ok := goKinds[kind]; ok {
		r.SetPrivateAttr(config.GazelleImportsKey, rule.PlatformStrings{Generic: value})
	} else if kind == "proto_library" {
		r.SetPrivateAttr(config.GazelleImportsKey, value)
	}
}
