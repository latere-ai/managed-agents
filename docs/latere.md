# The Latere runtime

For an application that runs sessions on the Latere platform. Each session is a hosted agent session with its own sandbox, on any model the platform offers, paid from the Latere wallet of the person who starts it. The runtime is a client of the platform's API and holds no model provider key.

```
go get latere.ai/x/managed-agents/latere
```

```go
import "latere.ai/x/managed-agents/latere"

runtime, err := latere.New(latere.Config{
    Product: latere.Product{Key: "myapp", Name: "My app"},
    Tokens:  latere.PersonalAccessToken("https://auth.latere.ai", os.Getenv("LATERE_API_KEY"), nil),
})
```

## Configuration

| Field | Default | What it is |
|---|---|---|
| `BaseURL` | `https://api.latere.ai` | The platform's API address. |
| `Product` | `managed` / `Managed session` | Your application's name. Agents are named `<key>-<digest>`, and the console labels the agent and the session with `Name`. The key is a lowercase word of letters, digits and hyphens. |
| `Tokens` | none | Whose wallet pays: the person a call acts for. See below. |
| `Image`, `Packages` | `base` and its packages | The machine image a session runs on, and the pip packages it already has. |
| `HTTP` | a plain client with a five-minute timeout | The client for every call. Pass your own to trace calls or change the timeout. |

## Acting for a person

Every call acts for one person, and the session runs in their account and is paid from their wallet. Give the runtime a way to get a bearer for `api.latere.ai`:

- **`PersonalAccessToken(authURL, key, client)`** exchanges a personal access token (`pat_...`) at Latere Auth and keeps the bearer until it is due. For a command-line tool run by the person themself.
- **`Command(command)`** runs a command that prints a bearer, in the manner of a git credential helper, and keeps what it printed for a few minutes. For borrowing a sign-in another Latere program holds.
- **`WithTokenSource(ctx, source)`** attaches a source to one call's context, and wins over `Config.Tokens`. For a server that runs many people's sessions through one runtime: put each session's person on the context of every call for that session.

A call with no source fails with `ErrNoCredential` before it reaches the platform.

## Models

`Models` lists the person's three Effort levels and the platform's catalog. A session asks for one of two things:

- **A preference**, `fast`, `balanced` or `best`, which runs on the person's Quick, Thorough or Sustained level. The level decides the model, so the session's model can change when the person changes their settings. `Efforts` reports which model each level runs on now and its price per million tokens.
- **An exact model**, as its provider and id, such as `anthropic` / `claude-sonnet-4.6`. A free variant sets `BillingVariant: "free"`.

`Status.Model` is the model the session actually started on.

## What it does and refuses

| | |
|---|---|
| Network | The person's network preference is the only network a session of theirs can have. `Capabilities` reads it and declares that mode alone: open when the preference is open. A specification asking for another mode is refused; the person changes the preference in the console. |
| Packages | Only pip packages the image already has. Any other is refused, since the platform cannot install one. Bootstrap commands are refused. |
| Grading | Supported. The worker is told to start a grader subagent with its own context and read-only tools (read, grep, glob) that grades the work against the rubric. A grade of needs revision sends the grader's findings back to the worker, up to `MaxRevisions` times; a failed grade, or a spent budget, ends the session failed. |
| Artifacts | The session's working folder, as the platform last saved it after a turn. Compiled Python files and the attachments a session was given are left out. |
| Attachments, inputs, repositories | Refused. The platform cannot place a file in a session yet. |
| Findings rounds | Not declared. |
| Cancel | Marks the session cancelled, so every process polling it sees the cancellation at once, and interrupts the running turn. |
| Release | Deletes the session. Its transcript and files are gone afterwards. |
| Restart | Resumable. The reference names the session by id; any process holding the person's credential reaches it again. |

## Cost

`Status.Usage` reports the tokens of every model call, the grader's included. `Usage.Extra[managed.MeterChargedCost]` is what the platform charged the person's wallet for the session, in micro-USD. It is already charged: an application that prices usage itself must not charge it again.

A session the wallet cannot pay for stops, and ends failed with a reason that says so.

## A real session

`TestRealLatereEndToEnd` runs one small graded session on the platform and charges the person a few cents. It is skipped unless `MANAGED_AGENTS_LATERE_E2E=1`, with `LATERE_API_KEY` (a personal access token) or `LATERE_TOKEN_COMMAND` (a command printing a bearer) set:

```bash
MANAGED_AGENTS_LATERE_E2E=1 LATERE_API_KEY=pat_... go -C latere test -run TestRealLatereEndToEnd -v ./...
```

`MANAGED_AGENTS_LATERE_MODEL` picks a preference or an exact `provider/id` model; the default is `balanced`.
