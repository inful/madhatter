Read the AGENTS.md for information regarding the project.

## TokenSave Usage Rules

- For code exploration questions (for example: "where is X", "how does Y work", "what calls Z"), call `mcp_tokensave_tokensave_context` first before manual grep/file reads.
- Use `mode="explore"` for discovery and `mode="plan"` for implementation planning.
- Provide focused `keywords` that match the feature being explored.
- Respect TokenSave call budget limits for this repository and do not exceed them.

## TokenSave Reporting Rules

- If a TokenSave tool call returns a `tokensave_metrics:` line, report the savings in the response.
- Include a short "TokenSave used" confirmation in exploration answers.
- If TokenSave cannot be used, explicitly state why and then proceed with standard file search/read tools.
