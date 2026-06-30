# My

My stores reusable agent memory and exposes it to local tools and agents.

## Language

**Source**:
Raw input that is saved before Memory is derived from it, such as a session transcript, hook event, or manual note.
Source is immutable; corrections are captured as later Source or Memory.
_Avoid_: Memory, event

**Memory**:
A curated unit of knowledge that an agent can reuse later.
Every Memory must come from at least one Source.
Memory can be revised when better interpretation is available.
_Avoid_: raw session, transcript, log

**Scope**:
The boundary that controls where Memory applies, such as a project, workspace, or session.
_Avoid_: namespace, tenant, global

**Link**:
A relationship between one Memory and another Memory.
_Avoid_: edge

**Record**:
To save Source and the Memory derived from it.
_Avoid_: add, ingest

**Recall**:
To find relevant Memory within a Scope.
_Avoid_: search, query

**Hook**:
A lifecycle command that lets an agent record or recall Memory around a session.
_Avoid_: sync
