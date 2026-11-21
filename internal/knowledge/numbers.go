package knowledge

import (
	"path"
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/model"
)

// NumberKey is the scope a decision number has to be unique in: one store
// (origin), one directory, and one id namespace.
//
// A decision's file name and its id usually carry the same number
// (0055-x.md, ADR-0055). Two records that share an id are DuplicateIDs' case.
// Two that share a number under different ids are a renumber done halfway:
// the id was changed and the file name was not, or the other way round. Then
// the next "highest number plus one" is read from the wrong file. The
// namespace keeps a store that numbers ADR-AUTH-0001 and ADR-PAY-0001 side by
// side in one directory from looking like a collision.
type NumberKey struct {
	Origin    string
	Dir       string
	Namespace string // the id without its trailing digits: "ADR-" for ADR-0055
	Number    string // the file name's leading digits: "0055" for 0055-x.md
}

// NumberKeyOf returns the key a record's number is unique under, and false
// for a file whose name does not start with digits and a dash. It reads only
// the strings it is given, so the same function keys a loaded object and an
// (id, path) pair read from another ref.
func NumberKeyOf(origin, id, p string) (NumberKey, bool) {
	base := path.Base(p)
	n := len(base) - len(strings.TrimLeft(base, "0123456789"))
	if n == 0 || n == len(base) || base[n] != '-' {
		return NumberKey{}, false
	}
	return NumberKey{
		Origin:    origin,
		Dir:       path.Dir(p),
		Namespace: strings.TrimRight(id, "0123456789"),
		Number:    base[:n],
	}, true
}

// DuplicateNumber is a decision number carried by records with different ids.
type DuplicateNumber struct {
	NumberKey
	Paths []string // every file under the key, sorted
	IDs   []string // the distinct ids they carry, sorted; always two or more
}

// DuplicateNumbers groups the decision records in objs by NumberKey and
// returns every number carried under two or more different ids, sorted. It
// is pure grouping with no I/O, like DuplicateIDs.
//
// A number whose files all carry ONE id is left out. That is a duplicated
// id, which DuplicateIDs reports in its own words, and one collision should
// give one finding. Records without an id are skipped for the same reason
// DuplicateIDs skips them.
func DuplicateNumbers(objs []model.KnowledgeObject) []DuplicateNumber {
	paths := map[NumberKey][]string{}
	ids := map[NumberKey]map[string]bool{}
	for _, o := range objs {
		if o.Type != model.KindDecision || o.ID == "" {
			continue
		}
		k, ok := NumberKeyOf(o.Origin, o.ID, o.Path)
		if !ok {
			continue
		}
		paths[k] = append(paths[k], o.Path)
		if ids[k] == nil {
			ids[k] = map[string]bool{}
		}
		ids[k][o.ID] = true
	}
	var out []DuplicateNumber
	for k, ps := range paths {
		if len(ids[k]) < 2 {
			continue
		}
		sort.Strings(ps)
		d := DuplicateNumber{NumberKey: k, Paths: ps}
		for id := range ids[k] {
			d.IDs = append(d.IDs, id)
		}
		sort.Strings(d.IDs)
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].NumberKey, out[j].NumberKey
		if a.Origin != b.Origin {
			return a.Origin < b.Origin
		}
		if a.Dir != b.Dir {
			return a.Dir < b.Dir
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Number < b.Number
	})
	return out
}

// Describe words a duplicate number for a finding or a doctor line.
func (d DuplicateNumber) Describe() string {
	where := d.Dir
	if d.Origin != "" {
		where = d.Origin + ":" + d.Dir
	}
	return "decision number " + d.Number + " in " + where + " is carried by " +
		strings.Join(d.Paths, ", ") + " under different ids (" + strings.Join(d.IDs, ", ") + ")"
}
