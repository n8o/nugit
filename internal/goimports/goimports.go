// Package goimports analyzes Go source dependencies. This is the bootstrapping
// "spike" from the re-shape: it proves the file->component mapping can be driven
// from a real import graph by reading nugit's own packages via go/parser.
//
// It is deliberately Go-only for the keystone. Other languages plug in later
// behind the same Analyzer shape (changed file -> set of internal import dirs).
package goimports

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// ModulePath reads the `module` line from go.mod at repoDir.
func ModulePath(repoDir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(repoDir, "go.mod"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")), nil
		}
	}
	return "", nil
}

// InternalDirs returns the repo-relative directories imported by the Go file at
// repo-relative `relPath`, restricted to imports within `modulePath` (i.e. the
// project's own packages). External and stdlib imports are dropped because they
// cannot map to a local C4 component.
//
// A non-Go or unreadable file yields an empty slice and no error, so callers can
// pass the full changed-file set blindly.
func InternalDirs(repoDir, modulePath, relPath string) ([]string, error) {
	if !strings.HasSuffix(relPath, ".go") {
		return nil, nil
	}
	abs := filepath.Join(repoDir, relPath)
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, abs, nil, parser.ImportsOnly)
	if err != nil {
		// A file that does not parse (deleted, mid-edit) contributes nothing.
		return nil, nil
	}
	var dirs []string
	seen := map[string]bool{}
	prefix := modulePath + "/"
	for _, imp := range f.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		dir := strings.TrimPrefix(p, prefix) // module-root-relative dir == repo-relative dir
		if !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	return dirs, nil
}
