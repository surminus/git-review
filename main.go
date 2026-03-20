// git-review: a local diff annotation tool for reviewing git diffs with
// line-level comments.
//
// Install to somewhere on $PATH (e.g. /usr/local/bin/git-review) to use
// as a git subcommand: git review annotate, git review show, etc.
package main

import (
	"bufio"
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

func loadAnnotations() (annotations, error) {
	p, err := reviewFilePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return make(annotations), nil
		}
		return nil, err
	}
	var a annotations
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("corrupt review.json: %w", err)
	}
	return a, nil
}

func saveAnnotations(a annotations) error {
	p, err := reviewFilePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0644)
}

// hunk represents a single diff hunk with its context.
type hunk struct {
	file    string
	header  string
	newStart int
	lines   []string // raw diff lines within the hunk (including +/-/space prefixes)
}

// parseDiff splits raw unified diff output into hunks, tracking which file
// each hunk belongs to.
func parseDiff(diff string) []hunk {
	var hunks []hunk
	var currentFile string
	var current *hunk

	hunkRegex := regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

	for _, line := range strings.Split(diff, "\n") {
		// Track current file from the +++ header, which works regardless
		// of diff.noprefix or a/b prefix settings.
		if strings.HasPrefix(line, "+++ ") {
			name := strings.TrimPrefix(line, "+++ ")
			name = strings.TrimPrefix(name, "b/") // strip optional b/ prefix
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
			start, _ := strconv.Atoi(m[1])
			current = &hunk{
				file:     currentFile,
				header:   line,
				newStart: start,
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

// newLineNumbers returns the new-file line numbers present in this hunk,
// in order. Only lines that appear in the new file (context and additions)
// are included.
func (h hunk) newLineNumbers() []int {
	var nums []int
	lineNo := h.newStart
	for _, l := range h.lines {
		if len(l) == 0 {
			continue
		}
		switch l[0] {
		case '+', ' ':
			nums = append(nums, lineNo)
			lineNo++
		case '-':
			// removed line, doesn't exist in new file
		}
	}
	return nums
}

// formatHunk returns a human-readable string for the hunk, with new-file
// line numbers in the margin for context and added lines.
func (h hunk) formatHunk() string {
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n", h.file)
	fmt.Fprintf(&b, "%s\n", h.header)

	lineNo := h.newStart
	for _, l := range h.lines {
		if len(l) == 0 {
			continue
		}
		switch l[0] {
		case '+':
			fmt.Fprintf(&b, "%4d %s\n", lineNo, l)
			lineNo++
		case '-':
			fmt.Fprintf(&b, "     %s\n", l)
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

// cmdAnnotate walks through hunks interactively, prompting the user to
// attach comments.
func cmdAnnotate(args []string) error {
	diff, err := getDiff(args)
	if err != nil {
		return err
	}
	if strings.TrimSpace(diff) == "" {
		fmt.Println("No diff to annotate.")
		return nil
	}

	hunks := parseDiff(diff)
	if len(hunks) == 0 {
		fmt.Println("No hunks found in diff.")
		return nil
	}

	a, err := loadAnnotations()
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(os.Stdin)
	added := 0

	for _, h := range hunks {
		fmt.Println()
		fmt.Println(h.formatHunk())

		lineNums := h.newLineNumbers()
		if len(lineNums) == 0 {
			continue
		}

		// Show available line range
		fmt.Printf("  Lines %d–%d in %s\n", lineNums[0], lineNums[len(lineNums)-1], h.file)
		fmt.Printf("  Add comment for %s:<line>? [line number to comment, or Enter to skip] ", h.file)

		for {
			if !scanner.Scan() {
				break
			}
			input := strings.TrimSpace(scanner.Text())
			if input == "" {
				break
			}

			lineNo, err := strconv.Atoi(input)
			if err != nil {
				fmt.Printf("  Not a valid line number. Try again or Enter to skip: ")
				continue
			}

			// Check the line is within this hunk's range
			found := false
			for _, n := range lineNums {
				if n == lineNo {
					found = true
					break
				}
			}
			if !found {
				fmt.Printf("  Line %d isn't in this hunk. Try again or Enter to skip: ", lineNo)
				continue
			}

			fmt.Printf("  Comment for %s:%d: ", h.file, lineNo)
			if !scanner.Scan() {
				break
			}
			comment := strings.TrimSpace(scanner.Text())
			if comment == "" {
				fmt.Printf("  Empty comment, skipping. Another line or Enter to move on: ")
				continue
			}

			key := fmt.Sprintf("%s:%d", h.file, lineNo)
			a[key] = comment
			added++
			fmt.Printf("  Noted. Another line or Enter to move on: ")
		}
	}

	if added > 0 {
		if err := saveAnnotations(a); err != nil {
			return err
		}
		fmt.Printf("\nSaved %d annotation(s).\n", added)
	} else {
		fmt.Println("\nNo annotations added.")
	}
	return nil
}

// cmdShow prints the diff with annotations inlined after the relevant lines.
func cmdShow(args []string) error {
	diff, err := getDiff(args)
	if err != nil {
		return err
	}
	if strings.TrimSpace(diff) == "" {
		fmt.Println("No diff.")
		return nil
	}

	a, err := loadAnnotations()
	if err != nil {
		return err
	}
	if len(a) == 0 {
		fmt.Println("No annotations found. Showing plain diff.")
		fmt.Println()
		fmt.Print(diff)
		return nil
	}

	hunks := parseDiff(diff)

	for _, h := range hunks {
		fmt.Printf("--- %s\n", h.file)
		fmt.Println(h.header)

		lineNo := h.newStart
		for _, l := range h.lines {
			if len(l) == 0 {
				continue
			}
			switch l[0] {
			case '+':
				fmt.Printf("%4d %s\n", lineNo, l)
				key := fmt.Sprintf("%s:%d", h.file, lineNo)
				if c, ok := a[key]; ok {
					fmt.Printf("     # REVIEW: %s\n", c)
				}
				lineNo++
			case '-':
				fmt.Printf("     %s\n", l)
			case ' ':
				fmt.Printf("%4d %s\n", lineNo, l)
				key := fmt.Sprintf("%s:%d", h.file, lineNo)
				if c, ok := a[key]; ok {
					fmt.Printf("     # REVIEW: %s\n", c)
				}
				lineNo++
			}
		}
		fmt.Println()
	}

	return nil
}

// cmdApply outputs a self-contained prompt to stdout with the diff and all
// annotations, suitable for piping into an AI coding agent.
func cmdApply(args []string) error {
	diff, err := getDiff(args)
	if err != nil {
		return err
	}
	if strings.TrimSpace(diff) == "" {
		return fmt.Errorf("no diff to apply")
	}

	a, err := loadAnnotations()
	if err != nil {
		return err
	}
	if len(a) == 0 {
		return fmt.Errorf("no annotations found — nothing to apply")
	}

	fmt.Println("I have a git diff with review comments. Please apply the feedback from each annotation to the corresponding file and line. Here is the full diff:")
	fmt.Println()
	fmt.Println("```diff")
	fmt.Print(diff)
	fmt.Println("```")
	fmt.Println()
	fmt.Println("Here are the review annotations to action:")
	fmt.Println()

	// Group annotations by file for clarity
	byFile := make(map[string][]struct {
		line    int
		comment string
	})
	for key, comment := range a {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 {
			continue
		}
		file := parts[0]
		line, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		byFile[file] = append(byFile[file], struct {
			line    int
			comment string
		}{line, comment})
	}

	for file, comments := range byFile {
		fmt.Printf("### %s\n\n", file)
		for _, c := range comments {
			fmt.Printf("- **Line %d**: %s\n", c.line, c.comment)
		}
		fmt.Println()
	}

	fmt.Println("Please address each annotation by modifying the relevant file at the specified line. Keep changes minimal and focused on the feedback.")

	return nil
}

// cmdClear removes the review.json file.
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
  apply    [diff args]   Output a prompt with the diff and annotations (pipe to an AI agent)
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
	case "apply":
		err = cmdApply(rest)
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
