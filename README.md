# git-review

Review git diffs hunk by hunk and attach comments, then pipe them
into an AI coding agent to action the feedback. It's designed for the
workflow where you're iterating on code with an AI and want to steer
changes precisely, without having to describe file paths and line
numbers by hand.

Annotations are stored in `.git/review.json`, scoped by diff content,
so they stay local and never get committed.

## Install

Download the latest binary from
[releases](https://github.com/surminus/git-review/releases) and put
it somewhere on your `$PATH`:

```
curl -Lo git-review https://github.com/surminus/git-review/releases/latest/download/git-review-linux-amd64
chmod +x git-review
mv git-review /usr/local/bin/
```

On macOS, swap `linux-amd64` for `darwin-arm64` (Apple Silicon) or
`darwin-amd64`.

Git will pick it up as a subcommand automatically: `git review`.

## Usage

### Annotate

Walk through the current diff hunk by hunk and attach comments:

```
git review annotate
git review annotate --cached
git review annotate HEAD~3
```

Any arguments after the subcommand are passed straight through to
`git diff`. For each hunk you can type a comment, `s` to split the
hunk into smaller pieces, `q` to quit, or Enter to skip.

If you have `interactive.diffFilter` configured (e.g.
`delta --color-only`), hunks are displayed through it, same as
`git add -p`.

### Show

Print the diff with your annotations, piped through your configured
pager:

```
git review show
git review show --cached
```

### Prompt

Output only the annotated hunks with comments inlined, ready to pipe
into an AI agent:

```
git review prompt | claude
git review prompt --cached | claude
```

### Clear

Wipe all annotations:

```
git review clear
```
