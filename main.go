// git-review: a local diff annotation tool for reviewing git diffs with
// hunk-level comments.
//
// Install to somewhere on $PATH (e.g. /usr/local/bin/git-review) to use
// as a git subcommand: git review annotate, git review show, etc.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// reviewFilePath returns the absolute path to .git/review.json for the
// current repository.
func reviewFilePath() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--git-dir").Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository (or git not installed)")
	}
	gitDir := strings.TrimSpace(string(out))
	abs, err := filepath.Abs(filepath.Join(gitDir, "review.json"))
	if err != nil {
		return "", err
	}
	return abs, nil
}

// annotations is a map of "file:line" -> comment.
type annotations map[string]string

// scopedAnnotations maps a diff hash to its annotations.
type scopedAnnotations map[string]annotations

func loadAllAnnotations() (scopedAnnotations, error) {
	p, err := reviewFilePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return make(scopedAnnotations), nil
		}
		return nil, err
	}
	var sa scopedAnnotations
	if err := json.Unmarshal(data, &sa); err != nil {
		return nil, fmt.Errorf("corrupt review.json: %w", err)
	}
	return sa, nil
}

func loadAnnotations(scope string) (annotations, error) {
	all, err := loadAllAnnotations()
	if err != nil {
		return nil, err
	}
	a, ok := all[scope]
	if !ok {
		return make(annotations), nil
	}
	return a, nil
}

func saveAnnotations(scope string, a annotations) error {
	all, err := loadAllAnnotations()
	if err != nil {
		return err
	}
	all[scope] = a

	p, err := reviewFilePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0644)
}

// hunk represents a single diff hunk with its context.
type hunk struct {
	file     string
	header   string
	oldStart int
	newStart int
	lines    []string // raw diff lines within the hunk (including +/-/space prefixes)
}

// parseDiff splits raw unified diff output into hunks, tracking which file
// each hunk belongs to.
func parseDiff(diff string) []hunk {
	var hunks []hunk
	var currentFile string
	var current *hunk

	hunkRegex := regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

	for _, line := range strings.Split(diff, "\n") {
		// Track current file from the +++ header, which works regardless
		// of diff.noprefix or a/b prefix settings.
		if name, ok := strings.CutPrefix(line, "+++ "); ok {
			name, _ = strings.CutPrefix(name, "b/") // strip optional b/ prefix
			if name != "/dev/null" {
				currentFile = name
			}
			continue
		}

		if m := hunkRegex.FindStringSubmatch(line); m != nil {
			// flush previous hunk
			if current != nil {
				hunks = append(hunks, *current)
			}
			oldStart, _ := strconv.Atoi(m[1])
			newStart, _ := strconv.Atoi(m[2])
			current = &hunk{
				file:     currentFile,
				header:   line,
				oldStart: oldStart,
				newStart: newStart,
			}
			continue
		}

		if current != nil {
			// collect lines that are part of the hunk body
			if len(line) > 0 && (line[0] == '+' || line[0] == '-' || line[0] == ' ') {
				current.lines = append(current.lines, line)
			}
		}
	}
	if current != nil {
		hunks = append(hunks, *current)
	}
	return hunks
}

// splitHunk splits a hunk into sub-hunks at change group boundaries.
// Returns nil if the hunk has only one change group (can't be split).
func splitHunk(h hunk) []hunk {
	// Identify change groups: contiguous runs of +/- lines.
	type changeGroup struct {
		startIdx int // index into h.lines
		endIdx   int // exclusive
	}

	var groups []changeGroup
	inChange := false
	for i, l := range h.lines {
		if len(l) == 0 {
			continue
		}
		isChange := l[0] == '+' || l[0] == '-'
		if isChange && !inChange {
			groups = append(groups, changeGroup{startIdx: i})
			inChange = true
		} else if !isChange && inChange {
			groups[len(groups)-1].endIdx = i
			inChange = false
		}
	}
	if inChange {
		groups[len(groups)-1].endIdx = len(h.lines)
	}

	if len(groups) <= 1 {
		return nil
	}

	// Precompute old/new line offsets for each index in h.lines.
	type lineOffset struct {
		oldLine int
		newLine int
	}
	offsets := make([]lineOffset, len(h.lines))
	oldLine := h.oldStart
	newLine := h.newStart
	for i, l := range h.lines {
		offsets[i] = lineOffset{oldLine, newLine}
		if len(l) == 0 {
			continue
		}
		switch l[0] {
		case '+':
			newLine++
		case '-':
			oldLine++
		case ' ':
			oldLine++
			newLine++
		}
	}

	// Build sub-hunks: each change group gets up to 3 lines of leading/trailing
	// context, capped so it doesn't overlap into adjacent change groups.
	const contextLines = 3
	var subHunks []hunk

	for gi, g := range groups {
		leadStart := max(g.startIdx-contextLines, 0)
		if gi > 0 {
			leadStart = max(leadStart, groups[gi-1].endIdx)
		}

		trailEnd := min(g.endIdx+contextLines, len(h.lines))
		if gi+1 < len(groups) {
			trailEnd = min(trailEnd, groups[gi+1].startIdx)
		}

		subLines := h.lines[leadStart:trailEnd]

		subOldStart := offsets[leadStart].oldLine
		subNewStart := offsets[leadStart].newLine

		var oldCount, newCount int
		for _, l := range subLines {
			if len(l) == 0 {
				continue
			}
			switch l[0] {
			case '+':
				newCount++
			case '-':
				oldCount++
			case ' ':
				oldCount++
				newCount++
			}
		}

		header := fmt.Sprintf("@@ -%d,%d +%d,%d @@", subOldStart, oldCount, subNewStart, newCount)

		linesCopy := make([]string, len(subLines))
		copy(linesCopy, subLines)

		subHunks = append(subHunks, hunk{
			file:     h.file,
			header:   header,
			oldStart: subOldStart,
			newStart: subNewStart,
			lines:    linesCopy,
		})
	}

	return subHunks
}

// firstNewLine returns the first new-file line number that's an addition
// (+ line) in this hunk. Falls back to newStart if there are no additions.
func (h hunk) firstNewLine() int {
	lineNo := h.newStart
	for _, l := range h.lines {
		if len(l) == 0 {
			continue
		}
		switch l[0] {
		case '+':
			return lineNo
		case ' ':
			lineNo++
		}
	}
	return h.newStart
}

// diffFilter returns the interactive.diffFilter config value, if set.
// This is the same filter git add -p uses (e.g. "delta --color-only").
func diffFilter() string {
	out, err := exec.Command("git", "config", "interactive.diffFilter").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// formatHunk returns a human-readable string for the hunk. If an
// interactive.diffFilter is configured (e.g. delta --color-only), the
// raw diff is piped through it, matching git add -p behaviour. Otherwise
// basic ANSI colours are used.
func (h hunk) formatHunk() string {
	// Build a raw diff fragment for the filter.
	var raw strings.Builder
	fmt.Fprintf(&raw, "--- a/%s\n", h.file)
	fmt.Fprintf(&raw, "+++ b/%s\n", h.file)
	fmt.Fprintln(&raw, h.header)
	for _, l := range h.lines {
		fmt.Fprintln(&raw, l)
	}

	if filter := diffFilter(); filter != "" {
		cmd := exec.Command("sh", "-c", filter)
		cmd.Stdin = strings.NewReader(raw.String())
		out, err := cmd.Output()
		if err == nil {
			return string(out)
		}
	}

	// Fallback: basic ANSI colours.
	const (
		red   = "\033[31m"
		green = "\033[32m"
		cyan  = "\033[36m"
		bold  = "\033[1m"
		reset = "\033[0m"
	)

	var b strings.Builder
	fmt.Fprintf(&b, "%s%s--- %s%s\n", bold, cyan, h.file, reset)
	fmt.Fprintf(&b, "%s%s%s\n", cyan, h.header, reset)

	lineNo := h.newStart
	for _, l := range h.lines {
		if len(l) == 0 {
			continue
		}
		switch l[0] {
		case '+':
			fmt.Fprintf(&b, "%4d %s%s%s\n", lineNo, green, l, reset)
			lineNo++
		case '-':
			fmt.Fprintf(&b, "     %s%s%s\n", red, l, reset)
		case ' ':
			fmt.Fprintf(&b, "%4d %s\n", lineNo, l)
			lineNo++
		}
	}
	return b.String()
}

func getDiff(extraArgs []string) (string, error) {
	args := append([]string{"diff"}, extraArgs...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return "", fmt.Errorf("git diff failed: %w", err)
	}
	return string(out), nil
}

// diffScope returns a SHA256 hash of the diff content, used to scope
// annotations so they don't cross-contaminate between branches or
// different diff ranges.
func diffScope(diff string) string {
	h := sha256.Sum256([]byte(diff))
	return hex.EncodeToString(h[:])
}

// resolvePager uses git's own pager resolution (GIT_PAGER -> core.pager
// -> PAGER -> less).
func resolvePager() string {
	out, err := exec.Command("git", "var", "GIT_PAGER").Output()
	if err != nil {
		return "less"
	}
	return strings.TrimSpace(string(out))
}

// isTerminal returns true if stdout is connected to a terminal.
func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// outputThroughPager sends text through the configured pager. The pager
// is invoked via sh -c, matching how git itself runs pagers.
func outputThroughPager(text string) error {
	pager := resolvePager()

	cmd := exec.Command("sh", "-c", pager)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		// If pager fails to start, just print directly
		fmt.Print(text)
		return nil
	}

	stdin.Write([]byte(text))
	stdin.Close()

	return cmd.Wait()
}

// cmdAnnotate walks through hunks interactively, prompting the user to
// attach comments per hunk.
func cmdAnnotate(args []string) error {
	diff, err := getDiff(args)
	if err != nil {
		return err
	}
	if strings.TrimSpace(diff) == "" {
		fmt.Println("No diff to annotate.")
		return nil
	}

	queue := parseDiff(diff)
	if len(queue) == 0 {
		fmt.Println("No hunks found in diff.")
		return nil
	}

	scope := diffScope(diff)
	a, err := loadAnnotations(scope)
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(os.Stdin)
	added := 0

	for len(queue) > 0 {
		h := queue[0]
		queue = queue[1:]

		fmt.Println()
		fmt.Println(h.formatHunk())
		lineNo := h.firstNewLine()
		fmt.Printf("  %s:%d — review hunk (empty to skip, s[plit], q[uit]): ", h.file, lineNo)

		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())

		quit := false
		switch strings.ToLower(input) {
		case "s":
			subHunks := splitHunk(h)
			if subHunks == nil {
				fmt.Println("  Can't split further (single change group).")
				queue = append([]hunk{h}, queue...)
			} else {
				queue = append(subHunks, queue...)
			}
		case "q":
			quit = true
		case "":
			// skip
		default:
			// anything else is a comment
			key := fmt.Sprintf("%s:%d", h.file, lineNo)
			a[key] = input
			added++
			fmt.Println("  Noted.")
		}
		if quit {
			break
		}
	}

	if added > 0 {
		if err := saveAnnotations(scope, a); err != nil {
			return err
		}
		fmt.Printf("\nSaved %d annotation(s).\n", added)
	} else {
		fmt.Println("\nNo annotations added.")
	}
	return nil
}

// cmdShow outputs the diff with annotations, piped through the user's
// configured pager when stdout is a terminal. Annotations are placed at
// hunk boundaries (not inside hunks) so pagers like delta can still
// parse the unified diff format.
func cmdShow(args []string) error {
	diff, err := getDiff(args)
	if err != nil {
		return err
	}
	if strings.TrimSpace(diff) == "" {
		fmt.Println("No diff.")
		return nil
	}

	scope := diffScope(diff)
	a, err := loadAnnotations(scope)
	if err != nil {
		return err
	}

	// No annotations: exec git diff directly so git uses its own pager
	// chain (delta, less, etc.) with full colour support.
	if len(a) == 0 {
		cmd := exec.Command("git", append([]string{"diff"}, args...)...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		return cmd.Run()
	}

	// Walk the raw diff, collecting annotations per hunk and flushing
	// them at hunk/file boundaries so we don't break the diff format.
	var buf strings.Builder
	var currentFile string
	var inHunk bool
	var lineNo int
	var pending []string

	hunkRe := regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

	flush := func() {
		for _, p := range pending {
			fmt.Fprintln(&buf, p)
		}
		pending = pending[:0]
	}

	for _, line := range strings.Split(diff, "\n") {
		isHunkHeader := hunkRe.MatchString(line)
		isFileHeader := strings.HasPrefix(line, "diff ") ||
			strings.HasPrefix(line, "index ") ||
			strings.HasPrefix(line, "--- ") ||
			strings.HasPrefix(line, "+++ ")

		if (isHunkHeader || isFileHeader) && len(pending) > 0 {
			flush()
		}

		if name, ok := strings.CutPrefix(line, "+++ "); ok {
			name, _ = strings.CutPrefix(name, "b/")
			if name != "/dev/null" {
				currentFile = name
			}
			inHunk = false
			fmt.Fprintln(&buf, line)
			continue
		}

		if m := hunkRe.FindStringSubmatch(line); m != nil {
			lineNo, _ = strconv.Atoi(m[1])
			inHunk = true
			fmt.Fprintln(&buf, line)
			continue
		}

		if isFileHeader {
			inHunk = false
			fmt.Fprintln(&buf, line)
			continue
		}

		if inHunk && len(line) > 0 {
			fmt.Fprintln(&buf, line)
			switch line[0] {
			case '+':
				key := fmt.Sprintf("%s:%d", currentFile, lineNo)
				if c, ok := a[key]; ok {
					pending = append(pending, fmt.Sprintf("# REVIEW [%s:%d]: %s", currentFile, lineNo, c))
				}
				lineNo++
			case ' ':
				key := fmt.Sprintf("%s:%d", currentFile, lineNo)
				if c, ok := a[key]; ok {
					pending = append(pending, fmt.Sprintf("# REVIEW [%s:%d]: %s", currentFile, lineNo, c))
				}
				lineNo++
			case '-':
				// deleted line, no new-file line number
			}
		} else {
			fmt.Fprintln(&buf, line)
		}
	}
	flush()

	output := buf.String()
	if isTerminal() {
		return outputThroughPager(output)
	}
	fmt.Print(output)
	return nil
}

// hunkHasAnnotation returns true if any line in the hunk has an annotation.
func hunkHasAnnotation(h hunk, a annotations) bool {
	lineNo := h.newStart
	for _, l := range h.lines {
		if len(l) == 0 {
			continue
		}
		switch l[0] {
		case '+', ' ':
			key := fmt.Sprintf("%s:%d", h.file, lineNo)
			if _, ok := a[key]; ok {
				return true
			}
			lineNo++
		}
	}
	return false
}

// cmdPrompt outputs a self-contained prompt to stdout with only the
// annotated hunks and their comments, suitable for piping into an AI
// coding agent.
func cmdPrompt(args []string) error {
	diff, err := getDiff(args)
	if err != nil {
		return err
	}
	if strings.TrimSpace(diff) == "" {
		return fmt.Errorf("no diff")
	}

	scope := diffScope(diff)
	a, err := loadAnnotations(scope)
	if err != nil {
		return err
	}
	if len(a) == 0 {
		return fmt.Errorf("no annotations found — nothing to prompt with")
	}

	// Filter hunks to only those with annotations.
	var annotated []hunk
	for _, h := range parseDiff(diff) {
		if hunkHasAnnotation(h, a) {
			annotated = append(annotated, h)
		}
	}

	fmt.Println("I have review comments on a git diff. Please apply the feedback from each annotation to the corresponding file and line.")
	fmt.Println()

	for _, h := range annotated {
		fmt.Println("```diff")
		fmt.Fprintf(os.Stdout, "--- a/%s\n", h.file)
		fmt.Fprintf(os.Stdout, "+++ b/%s\n", h.file)
		fmt.Println(h.header)
		lineNo := h.newStart
		for _, l := range h.lines {
			if len(l) == 0 {
				continue
			}
			fmt.Println(l)
			switch l[0] {
			case '+', ' ':
				key := fmt.Sprintf("%s:%d", h.file, lineNo)
				if c, ok := a[key]; ok {
					fmt.Printf("# REVIEW [%s:%d]: %s\n", h.file, lineNo, c)
				}
				lineNo++
			}
		}
		fmt.Println("```")
		fmt.Println()
	}

	fmt.Println("Please address each annotation by modifying the relevant file at the specified line. Keep changes minimal and focused on the feedback.")

	return nil
}

// cmdClear removes the review.json file (all scopes).
func cmdClear() error {
	p, err := reviewFilePath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil {
		if os.IsNotExist(err) {
			fmt.Println("Nothing to clear.")
			return nil
		}
		return err
	}
	fmt.Println("Cleared all annotations.")
	return nil
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: git-review <command> [args...]

Commands:
  annotate [diff args]   Walk through the diff hunk by hunk and add comments
  show     [diff args]   Print the diff with annotations inline
  prompt   [diff args]   Output a prompt with the diff and annotations (pipe to an AI agent)
  clear                  Remove all annotations

Any extra arguments after the subcommand are passed through to git diff
(e.g. git review annotate --cached, git review show HEAD~3).
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	rest := os.Args[2:]

	var err error
	switch cmd {
	case "annotate":
		err = cmdAnnotate(rest)
	case "show":
		err = cmdShow(rest)
	case "prompt":
		err = cmdPrompt(rest)
	case "clear":
		err = cmdClear()
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		usage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
