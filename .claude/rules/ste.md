---
paths:
  - "docs/**/*.md"
  - "*.md"
  - ".claude/rules/*.md"
  - ".claude/skills/**/*.md"
  - "**/*.go"
---

# Writing standard (STE)

Every spec file, issue body, comment, manual page and **code comment** in this project uses Simplified Technical English. The controlled vocabulary is the `## Terms` table in `docs/specs/spec.md`. Read that table before you write a domain word. If you need a word that is not in the table, add the word to the table. Use US English spelling.

The BCP 14 text of the Internet-Draft follows IETF conventions, and this rule does not apply to it.

## Keep these verbatim

- Anything quoted from a person.
- Evidence: error messages, logs, command output, test names, stack traces, `file:line` references.
- Code, config, commands, JSON, file paths, identifiers, label names.
- Third-party product names and API field names.

## Sentences

1. A procedure sentence is 20 words or fewer. A description sentence is 25 or fewer.
2. Write one instruction per sentence.
3. Write one topic per paragraph, with six sentences at most.
4. Put the condition first, then the action.
5. Put a warning before the step it applies to.

## Words

6. Use one word with one meaning and one part of speech.
7. Use one word for one concept. Never rotate synonyms.
8. Keep noun clusters to three words.
9. Use no metaphor, idiom or slang. State the mechanism.
10. Define each abbreviation once, in the Terms table.

## Grammar

11. Use active voice.
12. Use present tense for behavior. Use the imperative for instructions.
13. Keep the articles.
14. Use no `-ing` form as a noun or as a heading.
15. State what to do. Keep a prohibition only when the wrong action is likely and costly.
16. Use a vertical list for more than two conditions, parameters or alternatives.

## Patterns

**A functional requirement** is one testable statement, in active voice and present tense, with no conjunction. Write `FR-answers-11 — The TTL of that record is 86400 seconds.`

**An acceptance criterion** states one observable result, and not an implementation.

**An issue title** has an imperative verb and one deliverable, in 10 words or fewer.

**An issue body** uses one kind of information per heading: `Context`, `What to build`, `Acceptance criteria`, `Out of scope`.

**A code comment** has one fact per sentence and says why, not what.

```go
// Bad: increment the counter
// Good: The upstream path public/0 returns the latest round, so round 0 must not reach it.
```

**A doc comment** opens with one sentence that states the result. Then it states the parameters and the failure modes.

```go
// Round returns the verified round with number n.
// It returns ErrWithheld if the round time plus the grace time has passed with no record.
```

**A test name** states one behavior: `rejects a record with another round number`.

Keep marker keywords, write the body in STE, and name the issue: `// TODO(#412): Replace the fixed limit with the flag value.`

## Check before you write the file

- [ ] No sentence is longer than 25 words. No instruction is longer than 20.
- [ ] Every step holds one instruction.
- [ ] Every condition comes before its action. Every warning comes before its step.
- [ ] Every domain word is in the Terms table, with one meaning and one part of speech.
- [ ] No synonym rotation occurs.
- [ ] No noun cluster is longer than three words.
- [ ] No metaphor, idiom or undefined abbreviation occurs.
- [ ] The text uses active voice, present tense and articles, with no `-ing` nouns or headings.
- [ ] Lists carry anything with more than two conditions or parameters.
- [ ] Quotes, evidence, code, paths and identifiers are verbatim.
- [ ] Code comments state the reason. Doc comments open with the result.
