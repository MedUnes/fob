# Rust Transition Plan: `edged` Desktop Simulation Port

## 1. Mission Statement

**What:** Port the Go `edged` daemon to idiomatic desktop Rust, preserving the exact same simulation logic, FSM behavior, cryptographic signing, and mTLS transport — so that the Rust binary is a drop-in replacement that talks to the existing Go `stationd`.

**Who:** A developer with deep Go expertise (concurrency, FSM design, crypto, networking — evident from the `edged` codebase), who knows all Rust fundamentals but needs to build muscle memory for *idiomatic* Rust patterns. The gap is not syntax — it's "how Rust people do things."

**Guiding principle:** Every phase teaches a Go-to-Rust idiom transition through the real code being ported, not through toy exercises.

### Acceptance Criteria (for the overall mission)

- [ ] AC1: The Rust `edged` binary connects to the existing Go `stationd` over mTLS and sends signed events that the station accepts (HTTP 202)
- [ ] AC2: The FSM transitions (CONNECTED → AUTONOMOUS → DEGRADED → CONNECTED) behave identically to the Go version under the same network conditions
- [ ] AC3: Events cached during AUTONOMOUS mode are flushed correctly during DEGRADED mode with bounded concurrency
- [ ] AC4: The Rust simulator produces telemetry with the same physics model (drift, jitter, battery drain, altitude interpolation)
- [ ] AC5: The code is idiomatic Rust — no "Go written in Rust." Enums over int constants, `Result<T,E>` over sentinel errors, ownership over shared pointers, traits over implicit interfaces
- [ ] AC6: The Rust binary accepts the same CLI flags and reads the same SQLite database and certificate files as the Go binary

---

## 2. Architecture Brief: Why This Phase Order

The port is split into **6 phases** plus a Phase 0 (scaffold) and a final E2E phase. The ordering follows two rules:

1. **Dependencies flow downward** — each phase builds on the previous, never forward-references
2. **Idiom density increases gradually** — early phases teach struct/enum/serde patterns, later phases teach async, concurrency, and error propagation

```
Phase 0: Project scaffold (cargo workspace, module tree)
   │
Phase 1: Types + Event model (structs, serde, Display)
   │
Phase 2: FSM states + transitions (enums, match, state functions)
   │
Phase 3: Simulator (mutability, methods, math, rand)
   │
Phase 4: Crypto — Ed25519 signing (crate integration, hex encoding)
   │
Phase 5: Transport — HTTP client + mTLS (reqwest, rustls, error handling)
   │
Phase 6: Cache + concurrency (mpsc channels, tokio tasks, bounded concurrency)
   │
Phase 7: Main — wire everything, CLI parsing, SQLite, run loop
   │
Phase 8: E2E validation against Go stationd
```

**Why this order and not another:**

- **Phase 1 before Phase 2:** The FSM emits `Event` values. You need the type first.
- **Phase 2 before Phase 3:** The FSM calls `simulator.Tick()`. But you can stub it. Learning Rust enums + match on the FSM — the core of your program — is more valuable early than porting math.
- **Phase 3 before Phase 4:** Signing requires an event payload. The simulator produces the payload fields.
- **Phase 4 before Phase 5:** `sendEvent()` signs then sends. Get signing right in isolation before mixing in HTTP.
- **Phase 5 before Phase 6:** The cache flush in DEGRADED mode calls `sendEvent()`. You need transport working first.
- **Phase 6 before Phase 7:** Concurrency (burst flush) is the hardest Rust idiom shift from Go. Tackle it as its own phase, not buried in main wiring.

---

## 3. Phase 0 — Project Scaffold

### Goal
Set up a cargo workspace with idiomatic Rust project structure. No logic yet — just the skeleton.

### Why this matters
Go organizes code by directory (`internal/edge/`, `types/`, `cmd/edged/`). Rust organizes by crate and `mod` tree. If you replicate the Go layout, every subsequent phase will fight the module system. Get this right now.

### What to create

```
edged-rs/
├── Cargo.toml              # Workspace root
├── crates/
│   ├── edged/              # Binary crate (thin main.rs)
│   │   ├── Cargo.toml
│   │   └── src/
│   │       └── main.rs
│   └── edged-lib/          # Library crate (all logic lives here)
│       ├── Cargo.toml
│       └── src/
│           ├── lib.rs       # pub mod declarations
│           ├── event.rs     # Event struct (Phase 1)
│           ├── fsm.rs       # FSM states + transitions (Phase 2)
│           ├── simulator.rs # DroneSimulator (Phase 3)
│           ├── crypto.rs    # Ed25519 signing (Phase 4)
│           ├── transport.rs # HTTP + mTLS client (Phase 5)
│           ├── cache.rs     # Ring buffer + flush (Phase 6)
│           └── config.rs    # CLI config struct (Phase 7)
```

### Go → Rust idiom: Project structure

| Go | Rust | Why |
|---|---|---|
| `cmd/edged/main.go` — everything in one file | `main.rs` is a thin entry point that calls library code | Rust convention: binaries are glue, logic lives in a `lib` crate so it's testable and reusable |
| `internal/` — compiler-enforced privacy | `pub(crate)` visibility — Rust controls visibility at the item level, not the directory level | No need for `internal/` convention; just don't `pub` what shouldn't be exported |
| `go.mod` — flat dependency list | `Cargo.toml` workspace with member crates | Workspaces let you split code while sharing a single `target/` and lockfile |

### Crates to add to `edged-lib/Cargo.toml` (you'll use them across phases)

```toml
[dependencies]
serde = { version = "1", features = ["derive"] }
serde_json = "1"
ed25519-dalek = { version = "2", features = ["rand_core"] }
hex = "0.4"
reqwest = { version = "0.12", features = ["rustls-tls", "json"] }
tokio = { version = "1", features = ["full"] }
rand = "0.8"
rusqlite = { version = "0.31", features = ["bundled"] }
clap = { version = "4", features = ["derive"] }
tracing = "0.1"
tracing-subscriber = { version = "0.3", features = ["json"] }
thiserror = "2"
```

### How to verify
```bash
cargo build          # Compiles with no errors
cargo test           # Runs (empty) test suite
```

### AC
- [ ] `cargo build` succeeds
- [ ] `main.rs` prints "edged-rs starting" and exits
- [ ] Module files exist and are declared in `lib.rs` (can be empty with `// TODO` comments)

---

## 4. Phase 1 — Event Model

### Goal
Port `types/event.go` → `event.rs`. Your first real Rust struct with serialization.

### What to implement

**File: `crates/edged-lib/src/event.rs`**

Port the `Event` struct. In Go:
```go
type Event struct {
    EdgeID    string  `json:"edge_id"`
    Name      string  `json:"name"`
    Timestamp string  `json:"timestamp"`
    Lat       float64 `json:"lat"`
    Lon       float64 `json:"lon"`
    Alt       float64 `json:"alt"`
    State     string  `json:"state"`
    Battery   float64 `json:"battery"`
}
```

### Go → Rust idioms

| Go | Idiomatic Rust | Notes |
|---|---|---|
| Struct tags `` `json:"edge_id"` `` | `#[serde(rename = "edge_id")]` | Or use `#[serde(rename_all = "snake_case")]` on the struct if all fields follow the same pattern |
| `string` for timestamp | `String` — keep it as a string for now, same as Go | Later you could use `chrono`, but for port parity, match Go's `time.Now().Format(time.RFC3339Nano)` |
| Exported struct (capital letter) | `pub struct Event` | Rust uses `pub` keyword, not capitalization |
| No constructor convention | Implement a `fn new(...)` associated function | Rust convention for constructors. Not enforced, but idiomatic |
| Implicit `fmt.Println` via `%v` | `#[derive(Debug)]` for `{:?}` formatting | Always derive `Debug` on data structs. Add `Clone` too — events get cloned into the cache |

### Key Rust concept: Derive macros
```rust
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]  // Check if this matches your Go JSON — it should
pub struct Event { ... }
```
This single attribute replaces what Go does with struct tags + `encoding/json` reflection. Serde generates the serialization code at compile time — zero runtime reflection.

### How to test
Write a unit test in `event.rs`:
```rust
#[cfg(test)]
mod tests {
    // Serialize an Event to JSON, verify the keys match what stationd expects
    // Deserialize it back, verify round-trip equality
    // Verify the JSON output matches the Go version's format exactly
}
```

### AC
- [ ] `Event` struct serializes to JSON with the exact same field names as the Go version
- [ ] Round-trip serialize → deserialize produces the same struct
- [ ] `#[derive(Debug, Clone, Serialize, Deserialize)]` — no manual trait impls

---

## 5. Phase 2 — FSM States and Transitions

### Goal
Port `internal/edge/types.go` and the FSM transition logic from `cmd/edged/main.go`. This is where Rust diverges most from Go in expressiveness.

### What to implement

**File: `crates/edged-lib/src/fsm.rs`**

In Go, your states are `iota` integers with a name map:
```go
const (
    Connected  ServerState = iota  // 0
    Autonomous                      // 1
    Degraded                        // 2
)
var StateName = map[ServerState]string{...}
```

### Go → Rust idiom: This is the biggest single upgrade

| Go | Idiomatic Rust | Why it matters |
|---|---|---|
| `type ServerState int` + `iota` | `enum State { Connected, Autonomous, Degraded }` | Rust enums are sum types. The compiler enforces exhaustive matching — you cannot forget a state |
| `StateName` map for display | `impl std::fmt::Display for State` | No map lookup; the Display trait is the Rust way to convert to string |
| State functions returning next state: `func (e *Edge) connected() stateFn` | Two options discussed below | This is the core design decision |

### The FSM design decision

Your Go FSM uses the "state function" pattern — each state is a method that returns the next state function. This is elegant in Go. In Rust, you have two idiomatic options:

**Option A — Enum + match loop (recommended for this port):**
```rust
loop {
    state = match state {
        State::Connected => self.connected().await,
        State::Autonomous => self.autonomous().await,
        State::Degraded => self.degraded().await,
    };
}
```
This is the most common Rust FSM pattern. The compiler enforces that every state is handled. Each method returns the next `State`. Simple, readable, idiomatic.

**Option B — State function pointers (closer to Go pattern):**
Possible but fighting the borrow checker — function pointers that capture `&mut self` are painful. Don't do this.

**Go with Option A.** It maps naturally, the compiler helps you, and any Rust developer reading your code will immediately understand it.

### What to implement specifically

1. The `State` enum with `Display` impl (returns "CONNECTED", "AUTONOMOUS", "DEGRADED")
2. An `Edge` struct (equivalent to your Go `Edge` struct) holding config, cache, retry counter, client, simulator. Don't fill in all fields yet — use `todo!()` or placeholder types for fields from later phases
3. Method stubs: `async fn connected(&mut self) -> State`, `async fn autonomous(&mut self) -> State`, `async fn degraded(&mut self) -> State`
4. The `run` method containing the match loop

### Go → Rust idiom: Error handling in state transitions

In Go, your state functions use `if err != nil` and log errors. In Rust:
- State functions should return `State`, not `Result`. State transitions don't "fail" — they transition to a different state (AUTONOMOUS on error).
- Internal operations (send, probe) return `Result<T, E>` — and you match on Ok/Err to decide the next state.
- Use `thiserror` to define an error enum for edge operations.

### How to test
```rust
#[cfg(test)]
mod tests {
    // Test that Display for State matches Go's StateName map
    // Test FSM transition logic with mock/stub transport:
    //   - connected() returns Autonomous after N failed sends
    //   - autonomous() returns Degraded when probe succeeds
    //   - degraded() returns Connected after successful flush
    //   - degraded() returns Autonomous on flush failure
}
```

### AC
- [ ] `State` enum with `Display` — no integer constants, no lookup map
- [ ] `match` on state is exhaustive (compiler-enforced)
- [ ] State methods have correct signatures and return the right next state
- [ ] Error type defined with `thiserror`

---

## 6. Phase 3 — Drone Simulator

### Goal
Port `internal/edge/simulator.go` → `simulator.rs`. Physics simulation, mutability patterns, random number generation.

### What to implement

**File: `crates/edged-lib/src/simulator.rs`**

Port the full `DroneSimulator` struct and all methods: `new()`, `tick()`, `speed_up()`, `slow_down()`, `change_altitude()`, `head()`.

### Go → Rust idioms

| Go | Idiomatic Rust | Notes |
|---|---|---|
| `func (d *DroneSimulator) Tick()` (pointer receiver, mutates) | `fn tick(&mut self)` | `&mut self` is Rust's explicit "this method mutates." No pointer/value receiver confusion |
| `math.Cos(d.heading)` | `self.heading.cos()` | Rust's `f64` has math methods directly on the type. No `math.` package import needed |
| `rand.Float64()*jitter*2 - jitter` | `rng.gen_range(-JITTER..JITTER)` | Use `rand::thread_rng()` or pass an `&mut impl Rng` for testability. `gen_range` is more readable and idiomatic |
| Package-level constants: `const jitter = 0.00004` | Module-level constants: `const JITTER: f64 = 0.00004;` | Rust constants are SCREAMING_SNAKE_CASE. Must have explicit type |
| `math.Pi` | `std::f64::consts::PI` | Or just `PI` with a `use` import |
| `if d.Alt < 0 { d.Alt = 0 }` | `self.alt = self.alt.max(0.0)` | `f64::max()` / `f64::min()` — more idiomatic than if-clamp. Or `self.alt = self.alt.clamp(0.0, f64::MAX)` |

### Key Rust concept: Method naming

Go uses PascalCase for exported methods (`Tick`, `SpeedUp`). Rust uses snake_case for all functions and methods (`tick`, `speed_up`). The compiler will warn you if you use PascalCase for a function — listen to `clippy`.

### Key Rust concept: Randomness

In Go, `rand.Float64()` uses a global source. In Rust, you explicitly create an RNG:
```rust
use rand::Rng;
let mut rng = rand::thread_rng();
let jitter_x = rng.gen_range(-JITTER..JITTER);
```

For testability, you can accept `&mut impl Rng` as a parameter instead of creating one inside `tick()`. This is more idiomatic Rust (dependency injection via generics) but not mandatory for this port. Your call — if you want to write deterministic simulator tests, inject the RNG.

### How to test
```rust
#[cfg(test)]
mod tests {
    // Create simulator, call tick() N times, verify:
    //   - Position changes (lat/lon drift from initial Munich coordinates)
    //   - Battery drains at expected rate (0.0833% per tick)
    //   - Altitude interpolates toward target
    //   - Alt never goes negative
    //   - speed_up() increases step, slow_down() decreases (floor at 0)
}
```

### AC
- [ ] All 6 methods ported with snake_case naming
- [ ] Constants are `const SCREAMING_SNAKE: f64 = ...`
- [ ] Uses `rand` crate, not a hand-rolled RNG
- [ ] `f64` methods for math (`.cos()`, `.sin()`, `.max()`, `.abs()`) — no `math::` package equivalent
- [ ] Battery, altitude, and position logic matches Go exactly

---

## 7. Phase 4 — Ed25519 Signing

### Goal
Port the signing logic from `sendEvent()` in `main.go`. Specifically: sign a JSON byte payload with an Ed25519 private key, output the signature as a hex string.

### What to implement

**File: `crates/edged-lib/src/crypto.rs`**

Two functions:
1. Load/decode an Ed25519 private key from a base64 string (as stored in SQLite)
2. Sign a `&[u8]` payload, return the hex-encoded signature string

### Go → Rust idiom: Crate integration

In Go:
```go
sig := ed25519.Sign(e.simulator.PrivateKey, body)
signature := hex.EncodeToString(sig)
```

In Rust with `ed25519-dalek`:
```rust
use ed25519_dalek::{SigningKey, Signer};
let signature = signing_key.sign(payload);
let hex_sig = hex::encode(signature.to_bytes());
```

| Go | Idiomatic Rust | Notes |
|---|---|---|
| `ed25519.PrivateKey` (byte slice) | `ed25519_dalek::SigningKey` | Typed wrapper, not raw bytes. Rust libraries prefer newtypes over raw slices |
| `encoding/base64.StdEncoding.DecodeString()` | `base64::engine::general_purpose::STANDARD.decode()` | You'll need the `base64` crate — add to deps. Or use `data-encoding` crate |
| Error as second return value | `Result<SigningKey, CryptoError>` | Define a `CryptoError` variant in your error enum |

### Key Rust concept: Separation of concerns

In your Go code, the private key lives on the `DroneSimulator` struct. That's a design smell even in Go — the simulator does physics, not crypto. In Rust, keep the `SigningKey` on the `Edge` struct (or in a dedicated `Signer` wrapper), not on the simulator. This is your chance to clean up that coupling.

### Crate to add
```toml
base64 = "0.22"
```

### How to test
```rust
#[cfg(test)]
mod tests {
    // Generate a keypair, sign a known payload, verify the signature
    // Decode a base64 private key string, verify it produces a valid SigningKey
    // Verify hex encoding matches Go's hex.EncodeToString output format
}
```

### AC
- [ ] Private key decoded from base64 into `SigningKey` — not raw bytes floating around
- [ ] Sign function takes `&[u8]`, returns `String` (hex-encoded signature)
- [ ] Signing key lives on `Edge`, not on `DroneSimulator`
- [ ] Unit test verifies signature is valid using the corresponding `VerifyingKey`

---

## 8. Phase 5 — HTTP Transport + mTLS

### Goal
Port `sendEvent()` and `probeStation()` — the HTTP client with mTLS client certificate authentication.

### What to implement

**File: `crates/edged-lib/src/transport.rs`**

1. An HTTP client configured with mTLS (client cert + key, CA cert for server verification)
2. `send_event()`: POST JSON to `/api/v1/events` with `X-Signature` header, expect 202
3. `probe_station()`: GET `/healthz`, return `Ok(())` or error

### Go → Rust idiom: HTTP + TLS

In Go you manually build a `tls.Config` and inject it into `http.Client`. In Rust with `reqwest`:

```rust
let identity = reqwest::Identity::from_pem(&combined_pem)?;  // cert + key concatenated
let ca_cert = reqwest::Certificate::from_pem(&ca_pem)?;

let client = reqwest::Client::builder()
    .identity(identity)
    .add_root_certificate(ca_cert)
    .build()?;
```

| Go | Idiomatic Rust | Notes |
|---|---|---|
| `tls.Config{ Certificates, RootCAs, ... }` | `reqwest::ClientBuilder` with `.identity()` and `.add_root_certificate()` | `reqwest` abstracts the TLS config into a fluent builder |
| `http.NewRequest` + `client.Do()` | `client.post(url).header(...).body(...).send().await?` | `reqwest` is async and uses builder pattern |
| `resp.StatusCode != http.StatusAccepted` | `resp.status() != StatusCode::ACCEPTED` | Same concept, different syntax |
| `fmt.Errorf("unexpected status: %d", ...)` | Return a typed error: `TransportError::UnexpectedStatus(u16)` | Use your `thiserror` enum, not string formatting for errors |
| `defer resp.Body.Close()` | Automatic — `reqwest::Response` drops and cleans up | Rust's RAII (Drop trait) handles resource cleanup. No `defer` needed |

### Key Rust concept: `async` + `.await`

`reqwest` is async. Your `send_event` and `probe_station` will be `async fn`. This is why you added `tokio` — it's the async runtime. If this is your first time writing real async Rust:

- `async fn` returns a `Future` — nothing runs until you `.await` it
- This is conceptually similar to Go's `context.Context` cancellation model, but explicit. Every async call site has `.await` visible — no hidden concurrency
- The Go code uses blocking HTTP calls in goroutines. The Rust equivalent is async functions in tokio tasks.

### Key Rust concept: The `?` operator

In Go:
```go
resp, err := e.client.Do(req)
if err != nil {
    return fmt.Errorf("request failed: %w", err)
}
```

In Rust:
```rust
let resp = self.client.post(&url).send().await?;
```

The `?` operator propagates the error automatically if the function's return type is `Result`. This replaces 90% of Go's `if err != nil` blocks. The error is automatically converted via `From` trait (which `thiserror` implements for you).

### How to test
At this phase, test against your running Go `stationd`:
```bash
# Terminal 1: start your Go station
make run-station

# Terminal 2: run your Rust transport test
cargo test -- --ignored transport_integration
```

Write an integration test (marked `#[ignore]` so it doesn't run in CI) that:
- Creates an mTLS client using certs from `../storage/`
- Probes `/healthz` and asserts OK
- Sends a dummy event with a valid signature and asserts 202

### AC
- [ ] mTLS client loads certs from PEM files
- [ ] `send_event` sends JSON with `X-Signature` header, returns `Result<(), TransportError>`
- [ ] `probe_station` hits `/healthz`, returns `Result<(), TransportError>`
- [ ] No `unwrap()` in production code — all errors propagated with `?`
- [ ] Integration test passes against the Go `stationd`

---

## 9. Phase 6 — Event Cache + Bounded Concurrency

### Goal
Port the ring buffer cache (`e.cache` channel in Go) and the `burstFlush()` concurrent drain logic.

### What to implement

**File: `crates/edged-lib/src/cache.rs`**

1. A bounded event cache (Go uses a buffered channel; Rust has several options)
2. Non-blocking push (drop events if full, like Go's `select/default`)
3. Burst flush: drain the cache and send events concurrently with a bounded worker pool

### Go → Rust idiom: This is the hardest phase

| Go | Idiomatic Rust | Notes |
|---|---|---|
| `cache: make(chan types.Event, 1024)` | `tokio::sync::mpsc::channel(1024)` | Async bounded channel. The `Sender`/`Receiver` split maps to Go's channel semantics |
| Non-blocking send: `select { case e.cache <- ev: default: }` | `sender.try_send(ev)` — returns `Err` if full, don't propagate | `try_send` is the non-blocking equivalent |
| Receive with `for ev := range cache` | `while let Some(ev) = receiver.try_recv().ok()` to drain without blocking | Or `recv().await` if you want to block |
| `errgroup` with `.SetLimit(burstSize)` | `tokio::sync::Semaphore` + `tokio::task::JoinSet` | This is the key pattern — see below |

### Burst flush: Go vs Rust

Go:
```go
g, ctx := errgroup.WithContext(context.Background())
g.SetLimit(e.config.BurstSize)
for ev := range e.cache {
    g.Go(func() error { return e.sendEvent(ctx, ev) })
}
g.Wait()
```

Idiomatic Rust:
```rust
let semaphore = Arc::new(Semaphore::new(self.config.burst_size));
let mut join_set = JoinSet::new();

while let Ok(event) = self.cache_rx.try_recv() {
    let permit = semaphore.clone().acquire_owned().await?;
    let client = self.transport.clone();  // reqwest::Client is cheap to clone (Arc internally)
    join_set.spawn(async move {
        let result = client.send_event(&event).await;
        drop(permit);  // release semaphore slot
        result
    });
}

// Await all tasks, collect results
while let Some(result) = join_set.join_next().await {
    result??;  // JoinError? then TransportError?
}
```

### Key Rust concept: Ownership in concurrent contexts

In Go, goroutines can capture variables from the enclosing scope freely — the GC keeps everything alive. In Rust, `tokio::task::spawn` requires `'static` — you cannot borrow from `self`. This means:

- **Clone what the task needs** — `client.clone()`, `event.clone()` — and move it in
- Or use `Arc<T>` for shared ownership
- `reqwest::Client` uses `Arc` internally, so `.clone()` is just an atomic increment — cheap

This is the single biggest mental shift from Go concurrency. In Go you share by default and hope for the best (or use mutexes). In Rust the compiler forces you to decide: clone, move, or `Arc`. It feels restrictive at first but eliminates data races at compile time.

### Key Rust concept: `mpsc` Sender/Receiver split

In Go, a channel is one value you pass around. In Rust, `mpsc::channel()` returns `(Sender, Receiver)` — two separate types. The `Sender` can be cloned (multiple producers), but `Receiver` cannot (single consumer). This maps to your architecture: multiple state functions push events (via the `Sender`), and `burstFlush` drains them (via the `Receiver`).

The split also means `Sender` and `Receiver` can live in different struct fields or even different structs — you don't pass "the channel" around, you pass the half you need.

### How to test
```rust
#[cfg(test)]
mod tests {
    // Push 50 events into a cache of capacity 1024, flush, verify all sent
    // Push 2000 events into a cache of capacity 1024, verify 1024 cached (rest dropped)
    // Flush with burst_size=3, verify max 3 concurrent sends (use a counter + mutex)
    // Flush with simulated send failures, verify error returned
}
```

### AC
- [ ] Bounded channel with non-blocking push
- [ ] `burst_flush` uses `Semaphore` + `JoinSet` (not unbounded task spawning)
- [ ] No data races — compiler enforces this, but verify no `unsafe`
- [ ] Events are cloned/moved into tasks, not borrowed

---

## 10. Phase 7 — Main: Wire Everything Together

### Goal
Port `cmd/edged/main.go` — CLI parsing, SQLite config loading, initialization, and the FSM run loop.

### What to implement

**File: `crates/edged/src/main.rs`** (thin) + remaining logic in `crates/edged-lib/src/config.rs`

1. CLI flag parsing with `clap` (derive style)
2. SQLite database read with `rusqlite` — load edge config (keys, certs, name)
3. Initialize `Edge` struct with all components from prior phases
4. Probe station for initial state
5. Enter FSM run loop

### Go → Rust idioms

| Go | Idiomatic Rust | Notes |
|---|---|---|
| `flag.StringVar(&cfg.StationEndpoint, ...)` | `#[derive(Parser)]` with `clap` | Clap derive is declarative — struct fields become flags automatically |
| `slog.New(slog.NewJSONHandler(...))` | `tracing_subscriber::fmt().json().init()` | `tracing` is the Rust ecosystem's structured logging. Almost 1:1 with `slog` |
| `log.Info("message", "key", value)` | `tracing::info!(key = value, "message")` | Note: message is last in `slog`, also last in `tracing` macro. Key-value pairs first |
| `db.GetEdge(name)` | `rusqlite` query with `query_row` | See below |
| `os.Exit(1)` on fatal error | Return `Result` from `main()` — Rust prints the error and exits with code 1 | `fn main() -> Result<(), Box<dyn std::error::Error>>` or use `anyhow::Result` |
| `defer db.Close()` | Automatic — `Connection` implements `Drop` | RAII again. No deferred cleanup needed |

### Key Rust concept: `clap` derive macro

```rust
use clap::Parser;

#[derive(Parser)]
struct Config {
    #[arg(long = "edge-name")]
    edge_name: String,

    #[arg(long = "station-endpoint", default_value = "localhost:5002")]
    station_endpoint: String,

    #[arg(long = "send-interval", default_value_t = 2)]
    send_interval_sec: u64,
    // ...
}
```
This replaces all your `flag.XxxVar(...)` calls. Run `cargo run -- --help` and you get auto-generated help text.

### Key Rust concept: `rusqlite` for SQLite

```rust
let conn = Connection::open("../storage/drones.db")?;
let edge = conn.query_row(
    "SELECT id, name, private_key, mtls_cert, mtls_key FROM edges WHERE name = ?1",
    [&name],
    |row| {
        Ok(EdgeRow {
            id: row.get(0)?,
            name: row.get(1)?,
            private_key: row.get(2)?,
            mtls_cert: row.get(3)?,
            mtls_key: row.get(4)?,
        })
    },
)?;
```

Note `rusqlite` with the `bundled` feature compiles SQLite from source — no system dependency needed. This is also what makes it portable to ESP32 later (though on ESP32 you'd likely switch to NVS).

### Key Rust concept: `#[tokio::main]`

```rust
#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    // ...
}
```
This macro sets up the tokio async runtime. Equivalent to Go's runtime being always-on — but explicit.

### How to test
Run the full Rust binary against the Go station:
```bash
# Terminal 1
make run-station

# Terminal 2
cargo run --bin edged -- --edge-name="Aero" --station-endpoint="localhost:5002"
```
Watch the station logs — you should see events arriving from the Rust edge.

### AC
- [ ] CLI flags match the Go binary's flags exactly (same names, same defaults)
- [ ] Reads the same `./storage/drones.db` and `./storage/*.pem` files
- [ ] Probe determines initial state correctly
- [ ] FSM loop runs and transitions between states
- [ ] Structured JSON logging via `tracing`

---

## 11. Phase 8 — End-to-End Validation

### Goal
Prove the Rust `edged` is a drop-in replacement for the Go `edged` by running both against the same `stationd` and comparing behavior.

### Test Plan

**Test 1: Happy path — CONNECTED state**
1. Start Go `stationd` (`make run-station`)
2. Start Rust `edged` with `--edge-name="Aero"`
3. Verify: station logs show events arriving every 2 seconds
4. Verify: events have valid Ed25519 signatures (station accepts them — HTTP 202)
5. Verify: event JSON structure matches (same field names, same types)
6. Run for 60 seconds, confirm no errors

**Test 2: Network partition — CONNECTED → AUTONOMOUS → DEGRADED → CONNECTED**
1. Start station + Rust edge, let it connect
2. Kill the station (Ctrl+C)
3. Verify: Rust edge transitions to AUTONOMOUS, logs caching events
4. Wait 20 seconds (should cache ~10 events)
5. Restart the station
6. Verify: Rust edge transitions to DEGRADED, flushes cached events
7. Verify: station receives the burst of cached events
8. Verify: Rust edge transitions back to CONNECTED

**Test 3: Cache overflow**
1. Kill station, let Rust edge run in AUTONOMOUS for long enough to fill cache (1024 events at 2s interval = ~34 minutes, or reduce interval for testing)
2. Verify: oldest events are dropped, edge doesn't crash or block

**Test 4: Diff comparison**
1. Run Go edge and Rust edge simultaneously with different names (e.g., "Aero" for Go, "Bolt" for Rust)
2. Query the HTTP dashboard (`/api/v1/drones/live`)
3. Verify: both drones appear, both have valid telemetry, both show correct state

### AC (Mission-level)
- [ ] **AC1:** Station accepts Rust-signed events (HTTP 202) — same as Go
- [ ] **AC2:** FSM transitions match Go behavior under identical network conditions
- [ ] **AC3:** Cache flush works with bounded concurrency
- [ ] **AC4:** Simulator physics match (battery drain rate, position drift, altitude interpolation)
- [ ] **AC5:** Code passes `cargo clippy` with no warnings — idiomatic Rust
- [ ] **AC6:** Same CLI flags, same database, same cert files

---

## 12. Quick Reference: Go → Rust Idiom Cheat Sheet

Keep this handy while coding. These are the patterns that will feel wrong coming from Go but are correct in Rust.

| Go pattern | Rust equivalent | When you'll hit it |
|---|---|---|
| `if err != nil { return err }` | `?` operator | Everywhere |
| `err.Error()` | `thiserror` derive + `Display` impl | Error types |
| `fmt.Errorf("wrap: %w", err)` | `#[error("wrap: {0}")]` in thiserror enum | Error wrapping |
| `go func() { ... }()` | `tokio::spawn(async move { ... })` | Burst flush |
| `chan T` (bidirectional) | `mpsc::Sender<T>` + `mpsc::Receiver<T>` (split) | Cache |
| `select { case <-ctx.Done(): }` | `tokio::select! { _ = token.cancelled() => ... }` | Shutdown |
| `sync.Mutex` | `std::sync::Mutex` (or `tokio::sync::Mutex` for async) | Rarely needed — ownership replaces most mutex usage |
| `defer resource.Close()` | Automatic via `Drop` trait | DB connections, HTTP responses |
| `interface{}` / implicit interfaces | `trait` + explicit `impl Trait for Type` | If you extract transport as trait for testing |
| `make([]T, 0, n)` | `Vec::with_capacity(n)` | Preallocated buffers |
| `struct embedding` | Composition via fields (Rust has no embedding) | N/A — your Go code doesn't use embedding |
| `time.NewTicker(2 * time.Second)` | `tokio::time::interval(Duration::from_secs(2))` | FSM tick loop |
| `context.WithTimeout` | `tokio::time::timeout(duration, future)` | HTTP requests |
| `:=` (short variable declaration) | `let` (immutable) or `let mut` (mutable) | Everywhere — default to `let`, add `mut` only when needed |
