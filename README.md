# git-review

A local diff annotation tool. Review git diffs hunk by hunk, attach
line-level comments, then use those comments to guide code changes.

Annotations are stored in `.git/review.json`, so they stay local to
your repo and never get committed.

## Install

Build the binary and put it somewhere on your `$PATH` so git picks it
up as a subcommand:

```
go build -o git-review .
cp git-review /usr/local/bin/
```

Then use it as `git review`.

## Usage

### Annotate

Walk through the current diff hunk by hunk and attach comments to
specific lines:

```
git review annotate
```

You can pass any arguments you'd normally pass to `git diff`:

```
git review annotate --cached
git review annotate HEAD~3
```

For each hunk, you'll be shown the diff and can choose to [c]omment,
[s]plit the hunk into smaller pieces, or press Enter to skip.

### Show

Print the diff with your annotations inlined, marked with `# REVIEW:`:

```
git review show
git review show --cached
```

### Prompt

Output a self-contained prompt to stdout with the full diff and all
annotations, formatted for an AI coding agent:

```
git review prompt | claude
git review prompt --cached | claude
```

### Clear

Wipe all annotations:

```
git review clear
```
