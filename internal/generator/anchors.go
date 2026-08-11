package generator

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrAnchorNotFound is wrapped into the error InsertIntoContent returns when
// the requested anchor marker is absent, so callers can reliably detect this
// specific case (as opposed to e.g. a file read failure) with errors.Is and
// react accordingly, such as printing an AnchorFixHint.
var ErrAnchorNotFound = errors.New("anchor marker comment not found")

// normalizeLine collapses runs of internal whitespace to a single space and
// trims the ends, so two lines that differ only in spacing compare equal.
// gofmt re-aligns struct-tag and field whitespace after an insertion, which
// would otherwise defeat an exact-match idempotency check.
func normalizeLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// containsNormalizedBlock reports whether the normalized block lines appear as
// a contiguous run within the normalized content lines.
func containsNormalizedBlock(content string, block []string) bool {
	var want []string
	for _, l := range block {
		if n := normalizeLine(l); n != "" {
			want = append(want, n)
		}
	}
	if len(want) == 0 {
		return false
	}

	var have []string
	for _, l := range strings.Split(content, "\n") {
		if n := normalizeLine(l); n != "" {
			have = append(have, n)
		}
	}

	for i := 0; i+len(want) <= len(have); i++ {
		match := true
		for j := range want {
			if have[i+j] != want[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// isAnchorLine reports whether line is a gpsystem:* anchor comment, i.e.
// "// gpsystem:..." or "# gpsystem:..." (the two comment styles the
// templates use, for Go and YAML/dotenv respectively). This must not match
// on a bare "gpsystem:" substring: a rendered file could legitimately contain
// that text outside of an anchor comment too, e.g. as part of a host:
// container path in a --replace-dev Docker volume mount line, without being
// an anchor.
func isAnchorLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "// gpsystem:") || strings.HasPrefix(trimmed, "# gpsystem:")
}

// scopedContent returns the content lines from just after the nearest
// earlier anchor comment line (or the start of the file, when none) through
// lines[anchorIdx-1]. Some anchors repeat with a distinct marker per
// occurrence (gpsystem:deps-init(<module>), once per module in the shared
// cmd/worker/main.go) and their snippet text can legitimately be identical
// across occurrences (e.g. "Mailer: mailer," or
// "Dispatcher: outbox.NewDispatcher(...)" for two different modules).
// Checking idempotency against the whole file would then mistake a sibling
// module's field for this module's own and silently skip the insertion.
func scopedContent(lines []string, anchorIdx int) string {
	start := 0
	for i := anchorIdx - 1; i >= 0; i-- {
		if isAnchorLine(lines[i]) {
			start = i + 1
			break
		}
	}
	return strings.Join(lines[start:anchorIdx], "\n")
}

// InsertIntoContent inserts snippet into content immediately BEFORE the first
// line containing the anchor marker, re-indenting the snippet to the
// anchor's indentation. The anchor line is preserved so the result accepts
// future insertions. Returns (content, false, nil) unmodified when the
// snippet is already present (idempotent re-runs).
func InsertIntoContent(content, anchor, snippet string) (updated string, inserted bool, err error) {
	snippet = strings.Trim(snippet, "\n")

	lines := strings.Split(content, "\n")
	idx := -1
	indent := ""
	for i, line := range lines {
		if strings.Contains(line, anchor) {
			idx = i
			indent = line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			break
		}
	}
	if idx == -1 {
		return content, false, fmt.Errorf("anchor %q not found (was the marker comment removed?): %w", anchor, ErrAnchorNotFound)
	}

	var block []string
	for _, l := range strings.Split(snippet, "\n") {
		if l == "" {
			block = append(block, "")
			continue
		}
		block = append(block, indent+l)
	}

	// Idempotency: compare with internal whitespace collapsed, so a prior
	// insertion that gofmt later re-aligned (struct tags, field columns) is
	// still recognized as present and never duplicated. The search is scoped
	// to the section between the nearest earlier anchor and this one (see
	// scopedContent) so that per-occurrence anchors sharing a file don't
	// false-positive on identical snippet text a sibling section legitimately
	// also contains.
	if containsNormalizedBlock(scopedContent(lines, idx), block) {
		return content, false, nil
	}

	merged := append(lines[:idx:idx], append(block, lines[idx:]...)...)
	return strings.Join(merged, "\n"), true, nil
}

// anchorCommentPrefix returns the line-comment prefix a given file uses for
// gpsystem:* markers: "// " for Go and TypeSpec sources, "# " for YAML and
// dotenv files. This mirrors isAnchorLine's two recognized styles.
func anchorCommentPrefix(path string) string {
	switch filepath.Ext(path) {
	case ".yml", ".yaml", ".env":
		return "# "
	default:
		return "// "
	}
}

// AnchorFixHint renders the manual-fix message shown when an anchor marker is
// missing from path: the snippet that would have been inserted, followed by
// the marker line that must be restored just after it, so a future run can
// find the anchor again. The indentation is a plain 4-space guess since the
// real target location (and its surrounding indent) is unknown without the
// anchor to locate it.
func AnchorFixHint(path, anchor, snippet string) string {
	const indent = "    "
	prefix := anchorCommentPrefix(path)

	var block strings.Builder
	for _, l := range strings.Split(strings.Trim(snippet, "\n"), "\n") {
		if l == "" {
			block.WriteString("\n")
			continue
		}
		block.WriteString(indent + l + "\n")
	}

	return fmt.Sprintf(
		"\nnote: anchor %q not found in %s\nadd this manually (matching the surrounding indentation), then keep the marker line below it so future runs can find it again:\n\n%s%s%s\n\n",
		anchor, path, block.String(), indent, prefix+anchor,
	)
}

// InsertAtAnchor inserts snippet into the file immediately BEFORE the first
// line containing the anchor marker, re-indenting the snippet to the
// anchor's indentation. The anchor line is preserved so the file accepts
// future insertions. Returns (false, nil) without modifying the file when
// the snippet is already present (idempotent re-runs).
//
// In dry-run mode the planned insertion is reported but not written.
func (e *Engine) InsertAtAnchor(path, anchor, snippet string) (inserted bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("anchor target: %w", err)
	}
	content := string(data)

	updated, inserted, err := InsertIntoContent(content, anchor, snippet)
	if err != nil {
		if errors.Is(err, ErrAnchorNotFound) {
			fmt.Fprint(e.out(), AnchorFixHint(path, anchor, snippet))
		}
		return false, fmt.Errorf("%w in %s", err, path)
	}
	if !inserted {
		fmt.Fprintf(e.out(), "skip %s (snippet already present)\n", path)
		return false, nil
	}

	if e.DryRun {
		fmt.Fprintf(e.out(), "would insert %d line(s) at %q in %s\n", len(strings.Split(strings.Trim(snippet, "\n"), "\n")), anchor, path)
		return true, nil
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return false, err
	}
	fmt.Fprintf(e.out(), "updated %s (anchor %s)\n", path, anchor)
	return true, nil
}
