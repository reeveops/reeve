# Documentation screenshots

These are real GitHub comments from disposable `reeve-test` runs, captured September 16, 2026 (America/Chicago).
The captures are cropped; the comment content was not fabricated or edited.

| Image | Source | Reeve version and scope |
| --- | --- | --- |
| [Preview](preview.jpg) | [PR #23 comment](https://github.com/reeveops/reeve-test/pull/23#issuecomment-5708189691), [run](https://github.com/reeveops/reeve-test/actions/runs/35179684772) | `2fa7329b51b3322f30bcfc1c40251c0f9e07cd4b`; shared-workflow OpenTofu preview, two additions against empty disposable state. |
| [Apply timeline](apply-timeline.jpg) | [PR #24 comment](https://github.com/reeveops/reeve-test/pull/24#issuecomment-5708198194), [run](https://github.com/reeveops/reeve-test/actions/runs/35179629860) | `9cf21fa3de74c5c877ac72630e76e76fa12479ac`; live GitHub identities with local OpenTofu state, blocked then approved apply. |

The apply was driven by the trusted test harness in one job, not a human slash-command comment, and managed no cloud workload resources.
Both temporary PRs were closed and their branches removed after capture.

These images demonstrate the interface at the recorded versions, not acceptance of every later Reeve commit.
The `reeve-test` harness is being expanded; refresh screenshots when the visible interaction changes and record the source commit, PR, run, and scenario here.
