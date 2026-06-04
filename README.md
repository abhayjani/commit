# Commit

Commit enhances your WhatsApp usage by finding commitments automatically — things you said you'd do, and things others said they'd do. It auto detects when things are done.

When something goes quiet, it surfaces it for follow-up.

## How it works

You scan a QR code to link your WhatsApp account, same as WhatsApp Web. Commit runs on your machine. Every 10 seconds it reads new messages, sends them to the Claude API for analysis, and logs any commitments it finds.

The dashboard shows everything grouped by person: what you owe, what they owe, how long it's been. You can reply, set reminders, mark things done, or dismiss them.

## Features

- **Dashboard** — all open commitments in one view, grouped by chat. Filter by "I owe", "They owe", or "Resolved". Search across everything.
- **Auto-extraction** — Claude reads incoming messages every 10 seconds, identifies promises and obligations, and logs them with the original quote, person, and direction.
- **Auto-resolution** — when a commitment is fulfilled in conversation (a file shared, a task confirmed, someone says "done"), Commit marks it resolved automatically.
- **Follow-ups** — surfaces things others owe you that have gone quiet. Drafts a polite nudge message and lets you send it directly from the dashboard.
- **Reminders** — set a reminder on any commitment. When it's due, Commit sends you a WhatsApp message to your own account so it shows up in your chat list.
- **Favorites** — star important chats or commitments to pin them to a dedicated tab for quick access.
- **Reply from dashboard** — respond to any commitment's chat directly from the Commit UI without switching to WhatsApp.
- **WhatsApp bot commands** — message yourself on WhatsApp to check commitments, search, or mark things done (see commands below).
- **History sync** — when you link a new device, Commit backfills your recent WhatsApp message history so commitments appear immediately.
- **Dark and light theme** — toggle from the dashboard toolbar. Respects your preference across sessions.
- **Mobile web** — the dashboard is fully responsive. Access it from your phone's browser at the same local address.
- **Passcode protection** — the web interface is secured with a passcode. Your Claude API key is encrypted with AES-GCM on disk.

## Outscroll fork — read-only, with reply tracking + voice drafts

> **🔒 READ-ONLY: this fork can NEVER send a WhatsApp message.** Every send path
> (dashboard reply, reminders, welcome message, rate-limit notice, the bot) is
> hard-gated at a single chokepoint in `whatsapp/client.go` (`SendMessage`
> returns `ErrReadOnly`), and the auto-reply bot is disabled. The app only
> *reads* your chats and *drafts* text you copy yourself. This is the lowest ban
> risk this category allows — it behaves like a passive linked device (WhatsApp
> Web sitting open), with no automated sending. (Covered by tests:
> `TestReadOnlyBlocksSend`, `TestReplySendBlockedInReadOnly`.)

Built for running an agency over WhatsApp. Three things on top of the commitment engine:

**Reply tracking** — derived purely from message direction/timing, **no Claude API calls**, so it works the moment WhatsApp links, even with no API key:
- **Needs reply** — last message is *theirs*, you haven't responded. Your "who am I leaving hanging" queue, longest-waiting first.
- **Waiting on** — last message is *yours*, they've gone quiet past a threshold. Your chase list.

**Voice drafts** — on any item, **✨ Draft reply** writes a response in *your* voice (few-shot from your own past messages, preferring how you write to that specific person), then you **Copy** it and paste into WhatsApp yourself. Nothing is sent. Needs a Claude **or** OpenAI key (this is the only LLM cost on top of commitments).

**Bring your own provider** — Claude (Anthropic) or GPT (OpenAI), chosen in setup or Settings. Each provider keeps its own key + model; switching is instant. The whole LLM layer is one small `llm` package (`llm.Complete`), so extraction, nudges, and drafts all follow your choice. Default model is cheap on either side (Claude 3.5 Haiku / GPT-4o mini recommended).

Groups and muted chats are excluded from the reply queues by default. Commitments (Claude-powered) remain the general catch-all; their reply box is copy-only too.

### New endpoints
- `GET /api/replies` → `{ needs_reply: [...], awaiting_reply: [...] }`
- `POST /api/reply/draft` `{chat_jid}` → `{ draft }` (drafts only, never sends)
- `GET/POST /api/provider` → read/switch the active LLM provider (`anthropic` | `openai`)
- `GET /api/export` → all commitments + reply queues as one JSON blob (the hook for piping into the Outscroll CRM)

### Tunable thresholds (`settings` table — define per your workflow)
| Setting | Default | Meaning |
|---|---|---|
| `needs_reply_min_minutes` | `0` | hide inbound newer than this many minutes |
| `awaiting_reply_min_hours` | `12` | silence before "Waiting on" surfaces a chat |
| `replies_max_stale_days` | `60` | ignore threads with no activity past this |
| `replies_include_groups` | `0` | set `1` to include group chats |

### Launch flags
No admin password is requested by default (unlike upstream). Optional env vars:
- `COMMIT_SETUP_HOSTS=1` — add the cosmetic `commit` → `127.0.0.1` hosts entry (needs admin). Off by default; the app just uses `http://localhost:9384`.
- `COMMIT_HEADLESS=1` — don't auto-open a browser (for servers / scripts).

## System requirements

- **macOS** 12 Monterey or later (Apple Silicon or Intel)
- **Windows** 10 or later (64-bit)
- A [Claude API key](https://console.anthropic.com/) (Anthropic account required)
- WhatsApp account with multi-device support

## Install

### Mac (DMG)

Download `Commit-x.x.x.dmg` from Releases, open it, and drag Commit to Applications. Then run:

```bash
/Applications/Commit.app/Contents/MacOS/Commit
```

### Windows

Download `Commit-x.x.x-windows-amd64.zip` from Releases, extract it, and run `Commit.exe`.

### From source

Requires Go 1.22+ and a [Claude API key](https://console.anthropic.com/).

```bash
git clone https://github.com/mitensampat/commit.git
cd commit
go build -o commit .
./commit
```

## Setup

Open [http://commit:9384](http://commit:9384) in your browser (or `localhost:9384`). The setup wizard will walk you through:

1. **Set a passcode** — protects the web interface and encrypts your API key
2. **Enter your Claude API key** — stored locally, encrypted with AES-GCM
3. **Scan the QR code** — links Commit to your WhatsApp account

Once connected, Commit scans incoming messages every 10 seconds and populates the dashboard.

## WhatsApp bot commands

Message yourself on WhatsApp to interact with Commit:

| Command | Description |
|---------|-------------|
| `commitments` | List all open commitments |
| `owe @person` | Show what you owe someone |
| `done <text>` | Mark a commitment as resolved |
| `search <query>` | Find commitments by keyword |
| `help` | Show available commands |

## Architecture

```
main.go              — entry point, wires up components
server/              — HTTP server, API endpoints, auth
server/static/       — embedded web UI (single-page app)
extraction/          — Claude API client, commitment extraction prompt
store/               — SQLite database, message and commitment storage
whatsapp/            — WhatsApp client (whatsmeow), bot command handler
landing/             — marketing landing page
```

Data is stored in `~/.commit/`:
- `commit.db` — SQLite database (messages, commitments, settings)
- WhatsApp session files

## Building releases

```bash
# Mac DMG (ad-hoc signed, for local testing)
./scripts/build-mac.sh

# Mac DMG (signed + notarized, for distribution)
DEVELOPER_ID="Developer ID Application: ..." \
NOTARY_PROFILE="commit-notary" \
./scripts/build-mac.sh

# Windows zip
./scripts/build-windows.sh

# Both platforms
./scripts/build-all.sh
```

## Privacy & data

- All data stays on your machine in `~/.commit/` — messages, commitments, settings, and WhatsApp session files
- Messages are decrypted locally by the WhatsApp multi-device protocol
- Message content (including sender names and timestamps) is sent to the Claude API for commitment extraction — Commit does not filter or redact messages before sending
- Anthropic does not train on API inputs; their [data retention policy](https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching#data-retention-and-privacy) governs what happens on their side
- No cloud storage, no telemetry, no tracking by Commit
- Your WhatsApp linked device session persists until you unlink it from your phone (Settings → Linked Devices) — even if Commit is not running, messages will queue for the next session
- To fully remove Commit: unlink the device from WhatsApp, delete the app, and delete `~/.commit/`

## Third-party services

- **Claude API** (Anthropic) — commitment extraction from message content

## License

Private. Not open source.
