# Issue tracker: GitHub

Issues and PRDs for this repository live in GitHub Issues for
`cheng-zuguang/onlyoffice-gateway`. Use the `gh` CLI from this checkout.

## Conventions

- Create an issue with `gh issue create --title "..." --body "..."`.
- Read an issue, its labels, and comments with `gh issue view <number> --comments`.
- Apply workflow labels with `gh issue edit <number> --add-label "..."`.
- Publish implementation slices in dependency order, so later issues can link to
  their blockers.

When an engineering skill says to publish to the issue tracker, create a GitHub
Issue in this repository.
