package knowledge

import (
	"strings"
	"testing"
)

// nugit's own store is held to what its checks hold every store to: no two
// records share an id, and no two decisions share a number.
//
// The PR checks cannot promise this alone. They judge what a PR adds against
// the base as of the PR's last push, and a pull_request check does not re-run
// when the base moves. Two PRs that each minted ADR-0055 were both green and
// merged a minute apart. This test reads the whole tree, so the first
// `go test ./...` after such a merge fails, wherever it runs.
func TestOwnStoreHasNoDuplicateKeys(t *testing.T) {
	objs, err := Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) == 0 {
		t.Fatal("loaded no records from ../../.nugit; the test is not reading the repo's store")
	}
	for _, d := range DuplicateIDs(objs) {
		t.Errorf("%s is carried by %d files: %s", d.QualifiedID(), len(d.Paths), strings.Join(d.Paths, ", "))
	}
	for _, d := range DuplicateNumbers(objs) {
		t.Errorf("%s", d.Describe())
	}
}
