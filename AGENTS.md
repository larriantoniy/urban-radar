# Urban Radar development rules

## Development loop

`hypothesis → implementation → eval/tests → evidence → architecture decision → next experiment`

## Architecture

- Go is the deterministic control and runtime layer.
- PostgreSQL is the source of truth for persisted state.
- Hermes is the agent runtime and Telegram transport/UI.
- LLMs are for reasoning and content generation only.
- Never use LLM interpretation for explicit human approval or critical publication side effects.
- PostgreSQL authority takes precedence over agent memory or Hermes state.

## Change discipline

- Inspect existing code and interfaces before designing a new one.
- Do not add queues, workers, Redis, MCP, HTTP services, or similar infrastructure “for later”.
- Do not change validated prompts or policies without new evidence.
- Do not duplicate code-owned source-of-truth data in prompts.
- Use targeted tests while developing; run the full validation suite before a milestone verdict or commit.
- Do not commit without explicit user instruction.

## Safety and session context

- Never commit or print secrets.
- Use targeted, controlled execution for external side effects.
- Before requesting project history, read `AGENTS.md` and `docs/current-state.md`.
- For normal implementation work report concisely: Result, Changed, Evidence, Next. Use detailed architecture reports only for a new architecture decision.
