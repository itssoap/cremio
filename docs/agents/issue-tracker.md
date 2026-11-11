# Issue tracker: GitHub

Issues and specs for this repo live in GitHub Issues. Use the `gh` CLI.

- Read: `gh issue view <number> --json number,title,body,labels,comments`
- List: `gh issue list --state open`
- Comment: `gh issue comment <number> --body "..."`
- Close: comment with resolution, then `gh issue close <number>`
- Create: `gh issue create --title "..." --body "..."`

Infer the repo from `git remote -v`; verify the target with `gh repo view` before writing. Only file issues in `itssoap/cremio`.

PRs as a request surface: no.
