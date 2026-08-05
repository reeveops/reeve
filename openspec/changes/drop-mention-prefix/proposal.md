# Stop accepting `@reeve` as a default command prefix

## Why

`command-prefix` defaulted to `"/reeve,@reeve"`, so `@reeve apply` was a
documented, first-class way to drive reeve.

[github.com/reeve](https://github.com/reeve) is a real GitHub account
belonging to a real person. `@` in a GitHub comment is a mention, not
syntax — every `@reeve apply`, `@reeve plan`, `@reeve approve` comment in
every repo running reeve sent that person a notification about infrastructure
they have nothing to do with.

The mention style was never load-bearing. It was offered because `@name`
reads naturally for a bot, which is exactly the assumption that makes it
wrong here: reeve is a GitHub Action, not a GitHub App with an account. There
is no `@reeve` for a mention to resolve to except someone else's.

## What

`command-prefix` defaults to `"/reeve"`. The zero-config fallback in the
`pr_comment` approval source drops `@reeve` to match, so a deployment that
never sets the input and one that sets it explicitly agree.

Mention style stays supported as an opt-in — `command-prefix:
"/reeve,@my-org-bot"` — because an org that owns a handle has a legitimate
use for it. The docs point at that rather than at `@reeve`.

## Compatibility

Repos that relied on the default and use `@reeve ...` comments will find
those comments stop dispatching. The fix is one input:

```yaml
- uses: reeveops/reeve@master
  with:
    command-prefix: "/reeve,@reeve"
```

That is a deliberate opt-in to notifying an uninvolved account, which is the
right shape for this: it should be a choice someone makes, not a default they
inherit.
