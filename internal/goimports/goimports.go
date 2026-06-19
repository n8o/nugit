// Package goimports analyzes Go source dependencies. This is the bootstrapping
// "spike" from the re-shape: it proves the file->component mapping can be driven
// from a real import graph.
//
// It analyzes source PASSED IN (read from the reviewed ref), never the working
// tree, so the import graph matches base..head even when CI checks out a
// synthetic merge commit. _test.go files and build-constraint-excluded files do
// not drive architecture relationships.
package goimports

import (
	"go/build/constraint"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ModulePath reads the module path from go.mod at repoDir.
func ModulePath(repoDir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(repoDir, "go.mod"))
	if err != nil {
		return "", err
	}
	return ParseModulePath(string(b)), nil
}

// ParseModulePath extracts the module path from go.mod source.
func ParseModulePath(src string) string {
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

// Analyze returns the internal package directories imported by Go source `src`
// (repo-relative file relPath), restricted to imports within modulePath, plus
// whether the file participates in the host build. included=false for non-Go
// files, _test.go files, and files excluded by a //go:build / // +build
// constraint — none of those should drive architecture relationships.
func Analyze(modulePath, relPath, src string) (dirs []string, included bool) {
	if !strings.HasSuffix(relPath, ".go") {
		return nil, false
	}
	if strings.HasSuffix(filepath.Base(relPath), "_test.go") {
		return nil, false
	}
	if buildExcluded(src) {
		return nil, false
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, relPath, src, parser.ImportsOnly)
	if err != nil {
		return nil, true // unparseable mid-edit file: included, contributes no edges
	}
	prefix := modulePath + "/"
	seen := map[string]bool{}
	for _, imp := range f.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		var dir string
		switch {
		case p == modulePath:
			dir = "." // the repo-root package
		case strings.HasPrefix(p, prefix):
			dir = strings.TrimPrefix(p, prefix) // module-relative dir == repo-relative dir
		default:
			continue // stdlib or third-party: not a local component
		}
		if !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	return dirs, true
}

// buildExcluded reports whether a leading //go:build / // +build constraint
// excludes the file from the host build context (e.g. `//go:build ignore`).
func buildExcluded(src string) bool {
	for _, line := range leadingComments(src) {
		switch {
		case constraint.IsGoBuild(line):
			if expr, err := constraint.Parse(line); err == nil && !expr.Eval(hostTag) {
				return true
			}
		case constraint.IsPlusBuild(line):
			if expr, err := constraint.Parse(line); err == nil && !expr.Eval(hostTag) {
				return true
			}
		}
	}
	return false
}

// leadingComments returns the comment lines in the file header, stopping at the
// first non-comment, non-blank line (the package clause).
func leadingComments(src string) []string {
	var out []string
	for _, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "/*") || strings.HasPrefix(line, "*") {
			continue
		}
		if strings.HasPrefix(line, "//") {
			out = append(out, line)
			continue
		}
		break
	}
	return out
}

// hostTag evaluates a build tag against the host context. This intentionally
// matches the machine nugit runs on; cross-target evaluation is out of scope for
// the keystone (a known limitation noted in PLAN.md).
func hostTag(tag string) bool {
	switch tag {
	case runtime.GOOS, runtime.GOARCH, "gc":
		return true
	case "unix":
		return isUnix(runtime.GOOS)
	case "cgo":
		return os.Getenv("CGO_ENABLED") != "0"
	}
	return false
}

func isUnix(goos string) bool {
	switch goos {
	case "aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos",
		"ios", "linux", "netbsd", "openbsd", "solaris":
		return true
	}
	return false
}
