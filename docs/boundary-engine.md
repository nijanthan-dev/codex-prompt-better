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
narrow a result but cannot broaden a higher-authority denial or non-goal. Losing
candidates remain available as conflict evidence instead of being discarded.

Conflict references form a directed graph. Traversal and adjacency are sorted,
so the first reported cycle is stable. A high-risk unresolved conflict or cycle
fails closed during risk evaluation; lower-risk ambiguity requests clarification
or emits a warning according to the execution policy.

## Privacy and bounds

Discovery walks metadata only, never follows symlinks, and reads content only
from recognized applicable instruction files. It stops after 10,000 metadata
entries, 64 scope levels, or 64 KiB per instruction file. Public references are
stable hashes; diagnostics never include raw content or absolute paths.
