# Boundary engine

Boundary discovery is explicit, local, read-only, and platform-neutral. Adapters
emit normalized candidates; the core never receives an absolute host path.

## Deterministic precedence

Candidates sort by this tuple, highest first:

1. authority: host permission, explicit configuration, applicable instruction,
   user request, repository metadata, then derived default;
2. scope specificity;
3. confidence;
4. stable sanitized candidate ID.

Pack priority cannot override source authority. Lower-authority evidence may
narrow a result but cannot broaden a higher-authority denial or non-goal. Extension
packs therefore reject `continue` rules; they may only warn, clarify, or block. Losing
candidates remain available as conflict evidence instead of being discarded.

Conflict references form a directed graph. Traversal and adjacency are sorted,
so the first reported cycle is stable. A high-risk unresolved conflict or cycle
fails closed during risk evaluation; lower-risk ambiguity requests clarification
or emits a warning according to the execution policy.

Results are bounded to 256 decisions after evaluating every candidate. Selection
is deterministic: source authority first, then restrictive outcome, risk, candidate
specificity/confidence order, and lexical provenance. Extension volume cannot hide
a more restrictive built-in decision for the same candidate. Lint evaluates every
selected outcome while emitting at most 100 diagnostics.

## Privacy and bounds

Discovery walks metadata only, never follows symlinks, and reads content only
from recognized applicable instruction files. It stops after 10,000 metadata
entries, 64 scope levels, or 64 KiB per instruction file. Public references are
stable categorical identifiers; they contain no raw path, reversible/plain path
hash, key material, instruction content, username, or absolute path.
