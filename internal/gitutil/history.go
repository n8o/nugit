package gitutil

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// HistoryCommit is one commit with its graph position — what a history miner
// needs that model.Commit (message only) does not carry.
type HistoryCommit struct {
	SHA     string
	Parents []string
	Time    time.Time // committer time
	Subject string
	Body    string
}

// Message is the full commit message: subject, blank line, body.
func (c HistoryCommit) Message() string {
	if c.Body == "" {
		return c.Subject
	}
	return c.Subject + "\n\n" + c.Body
}

// History returns every commit reachable from ref (oldest first) with parents
// and committer time, plus the ref's first-parent chain (oldest first). One git
// call each: a miner walking thousands of commits must not spawn a process per
// commit to learn its parents.
func (r Repo) History(ref string) (all []HistoryCommit, firstParent []string, err error) {
	out, err := r.git("log", "--reverse", "--topo-order",
		"--pretty=format:%H"+fieldSep+"%P"+fieldSep+"%ct"+fieldSep+"%s"+fieldSep+"%b"+commitSep, ref)
	if err != nil {
		return nil, nil, err
	}
	for _, rec := range strings.Split(out, commitSep) {
		rec = strings.Trim(rec, "\n")
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, fieldSep, 5)
		if len(parts) < 5 {
			continue
		}
		ts, _ := strconv.ParseInt(parts[2], 10, 64)
		all = append(all, HistoryCommit{
			SHA:     parts[0],
			Parents: strings.Fields(parts[1]),
			Time:    time.Unix(ts, 0).UTC(),
			Subject: parts[3],
			Body:    strings.TrimSpace(parts[4]),
		})
	}
	fp, err := r.git("rev-list", "--reverse", "--first-parent", ref)
	if err != nil {
		return nil, nil, err
	}
	return all, strings.Fields(fp), nil
}

// CountTouching counts non-merge commits reachable from ref, committed in
// [since, until], that touched any of paths (git-root-relative, literal). An
// empty path list counts nothing rather than everything.
func (r Repo) CountTouching(ref string, since, until time.Time, paths []string) (int, error) {
	if len(paths) == 0 {
		return 0, nil
	}
	args := []string{"rev-list", "--count", "--no-merges",
		fmt.Sprintf("--since=@%d", since.Unix()), fmt.Sprintf("--until=@%d", until.Unix()), ref, "--"}
	for _, p := range paths {
		args = append(args, ":(top,literal)"+p)
	}
	out, err := r.git(args...)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// ShowFiles reads many paths at one ref through a single `git cat-file
// --batch`, so loading a whole store at a historical ref costs one process,
// not one per file. Semantics match ShowFile: a path absent at ref maps to "".
func (r Repo) ShowFiles(ref string, paths []string) (map[string]string, error) {
	out := make(map[string]string, len(paths))
	if len(paths) == 0 {
		return out, nil
	}
	var in bytes.Buffer
	for _, p := range paths {
		if strings.ContainsAny(p, "\n") {
			continue // cannot be expressed on a batch line; treated as absent
		}
		in.WriteString(ref + ":" + p + "\n")
	}
	cmd := exec.Command("git", "-C", r.Dir, "cat-file", "--batch")
	cmd.Stdin = &in
	var errb bytes.Buffer
	cmd.Stderr = &errb
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	br := bufio.NewReader(stdout)
	for _, p := range paths {
		if strings.ContainsAny(p, "\n") {
			out[p] = ""
			continue
		}
		header, rerr := br.ReadString('\n')
		if rerr != nil {
			_ = cmd.Wait()
			return nil, fmt.Errorf("git cat-file --batch: %w: %s", rerr, strings.TrimSpace(errb.String()))
		}
		f := strings.Fields(header)
		if len(f) < 3 || f[1] != "blob" {
			out[p] = "" // "<spec> missing", or a tree/submodule: not a file
			if len(f) == 3 {
				n, _ := strconv.Atoi(f[2])
				_, _ = io.CopyN(io.Discard, br, int64(n)+1)
			}
			continue
		}
		n, _ := strconv.Atoi(f[2])
		buf := make([]byte, n)
		if _, rerr := io.ReadFull(br, buf); rerr != nil {
			_ = cmd.Wait()
			return nil, fmt.Errorf("git cat-file --batch: %w", rerr)
		}
		_, _ = br.ReadByte() // trailing newline
		out[p] = string(buf)
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("git cat-file --batch: %w: %s", err, strings.TrimSpace(errb.String()))
	}
	return out, nil
}
