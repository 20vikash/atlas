# Metal

The root `atlas/CLAUDE.md` governs the code in this module.

For Go code, also follow the repository [Go anti-pattern rules](../llm/go-code-review-guide.md).

## Documentation

Use these rules when you write or update any `SPEC.md` or file under `docs/`.

Mandatory: Keep each Markdown prose paragraph on one source line. Do not add line breaks to wrap text. Start a new line only for a new paragraph, list item, heading, table row, or structured block.
Mandatory: Document current behavior only. Do not mention removed implementations, deleted interfaces, old commands, or previous behavior.

### Two layers

- Package `SPEC.md` is the detailed layer: types, ownership, call flow, and diagrams.
- `docs/*.md` is the broad layer: an overview with enough detail to orient a reader.
- Detail lives in exactly one place, the SPEC. The docs summarize and route to it.
- Cross-link both ways. Each `docs/*.md` section links to the owning SPEC for full detail. Each SPEC links to its concept doc for the broad picture.

### Diagrams

- Use ASCII only. Use boxes and arrows for flows, state machines, and dependency graphs. Use ASCII trees for filesystem and network-topology layouts.
- Give every broad structure, concept, or flow a diagram.

### Tone and words

- Write plain, direct text. Use only the prose that the reader needs.
- Use ASD-STE100 Simplified Technical English. Use short sentences and one term for one thing.
- Do not use em dashes. Use a colon, or split the sentence.
- Spell out “virtual machine” on its first use in a file Purpose. Use “VM” after that, and in tables, diagrams, and headings.
- Gloss a non-obvious external term, flag, or mode in one line when you first use it. Do not assume the reader knows it.
- Use binary units for every size and rate: MiB and MiB/s. Do not use MB, Mbps, or megabits. Name a field for its unit, such as `disk_mib` and `throughput_mibps`.

### SPEC structure

```text
# <package>: <one-line purpose>
[<parent> SPEC](../SPEC.md) · overview: [docs/<concept>.md](../../docs/<concept>.md)

## Purpose        1-2 sentences
## Types          key types and which type owns which state
## <Diagram(s)>   ASCII: state machine / flow / topology / dataset lineage
## Related        links to the concept doc and sibling SPECs
```

- Do not add a “Dependencies” section. Put dependency direction in the `internal/` graph and in Related links.
- Lead Purpose with the premise: the core idea before the mechanics. State it in one or two sentences.
- Put design rationale in the concept doc, not the SPEC. Use a “Design notes” section in the `docs/*.md` overview for one or two short rationale notes. The SPEC states the premise in Purpose and otherwise describes mechanics.
- A concept document can summarize the premise when it explains why the design matters. Keep implementation mechanics and detailed behavior in the SPEC. Do not repeat exact code behavior in both places.
- Parent and root SPEC files are routers. Keep them short. Link to children and concept docs.

### Keep in sync

Update the relevant SPEC and docs in the same pull request as any change to behavior, interface, operation, or layout. Metal is under active development.

### Commits

- Use `docs(<scope>): Sentence case` for documentation-only commits.
- Do not add an AI co-author or session metadata.
