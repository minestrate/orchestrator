# Contributing to minestrate

First, thank you for your interest in contributing to minestrate. :tada:

The following is a set of guidelines for contributing to minestrate, the Docker orchestrator for
Minecraft minigame servers. These are guidelines, and in general it is recommended to stick to
them when contributing, but use your best judgement.

### Issues
Issues are welcome, both as bug reports and feature requests.

When opening a bug report, include enough information to reproduce the problem: the server state
sequence leading up to it, relevant logs, and whether it involves a transition, an HTTP request, or
a Docker lifecycle event. When opening a feature request, be concrete about how the feature should
behave, particularly if it touches the FSM, the REST API, or the orchestrator's interaction with
Docker.

### Pull Requests
For non-trivial changes, especially anything touching `domain/fsm.go` or the orchestrator's
transition logic, open an issue first to discuss the approach before writing code.

When reviewing pull requests, we aim for:
* Maintaining the quality and testability of the codebase.
* Standard Go formatting (`go fmt`, `go vet`).
* Clear separation between domain, orchestration, and transport concerns.

Before opening a pull request:

* Run `go fmt ./...` and `go vet ./...` on any files you've changed.
* Provide documentation for exported symbols (`TypeName`, `FunctionName`). Unexported symbols
  should be documented unless trivial and self-explanatory.
* Keep packages honest about their layer:
    - `domain/` must not import `net/http`, Docker SDK types, or anything from `internal/allocator/`
      or `api/`. If your change requires this, the logic likely belongs in a different package.
    - `dockerclient/` owns the canonical Docker client interface and its mock. All packages that
      talk to the Docker API depend on this package. Do not define separate Docker client
      interfaces elsewhere.
    - `internal/allocator/` owns Docker network lifecycle (create, remove, inspect) behind
      `NetworkManager`. Container operations (create, start, stop, remove) are called through the
      `dockerclient.Client` interface from the orchestrator package.
    - `api/` owns request parsing, response shaping, and status code mapping. Domain errors
      (e.g. `ErrInvalidTransition`) are translated to HTTP status codes here, not in `domain/`.
* **FSM invariants are non-negotiable.** A server has exactly five lifecycle states, and state may
  only change via a validated transition. If your change adds a new state or transition path:
    - Update the transition table in `domain/fsm.go` and the table-driven tests in
      `domain/fsm_test.go` covering every legal and illegal transition pair, not just the new one.
    - Invalid transitions must return a typed domain error, which `api/` maps to `409
      Conflict`. Never return a generic error or a different status code for this case.
    - If the transition can be triggered concurrently (HTTP request racing a Docker lifecycle
      callback), make sure the FSM mutation is guarded — add a test that exercises concurrent
      transition attempts, not just sequential ones.
* Where possible, expose as few exported symbols as possible. If a type or function doesn't need
  to be used outside its package, keep it unexported.
* Return early to keep functions shallow and the main logic path easy to follow.
* Group three or more sequential variable declarations into a single `var ( )` block.
* Be conservative with generics — only use them where they meaningfully reduce duplication or
  complexity, particularly for exported APIs.
* Avoid partial features. If a pull request can't fully implement something (e.g. a new server
  type, a new transition path), mark the gap with `// TODO: ...` and explain what's missing.
* Test against a real Docker daemon for orchestrator-level changes, not just the mock, before
  opening the PR. State-machine and middleware changes should be covered by unit tests
  (`*_test.go`) with no live Docker dependency.

If you get stuck or want feedback on an approach before finishing a pull request, open a draft PR
or reach out via an issue — happy to work through it together.
