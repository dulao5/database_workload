# database_workload

English | [中文](README-zh.md) | [日本語](README-ja.md)

**A load generator that keeps sending SQL to MySQL / TiDB, driven by a JSON config.**
You write SQL templates and describe how each parameter is randomized; it runs N concurrent workers at the rate you set until you press Ctrl-C.

What it can simulate:

- **How the app connects**: long-lived connections (apps with a connection pool) or a new connection per transaction (apps without one).
- **How SQL is sent**: plain text queries, server-side prepared statements, or a whole transaction in one or two round trips (multi-statement / pipelined binary).
- **How parameters are distributed**: uniform, power-law (hotspots), partitioned power-law, plus correlations such as "several statements in one transaction share the same random id" or "each connection sticks to one table".

What it does **not** do:

- **It does not create data.** Create tables and load data first with `sysbench prepare` or [dbgen](https://github.com/kennytm/dbgen) — see [section 2](#2-quick-start).
- **It does not print QPS / latency.** The log only has start, stop and error lines. Read throughput and latency on the database side (TiDB Dashboard, Grafana, `information_schema.statements_summary`, …).

---

## Contents

1. [Execution model: what it actually does](#1-execution-model-what-it-actually-does)
2. [Quick start](#2-quick-start)
3. [Top-level settings, one by one](#3-top-level-settings-one-by-one)
4. [Execution modes at a glance (what goes over the wire per transaction)](#4-execution-modes-at-a-glance-what-goes-over-the-wire-per-transaction)
5. [Combinations that don't work together](#5-combinations-that-dont-work-together)
6. [SQL templates](#6-sql-templates)
7. [How params are generated](#7-how-params-are-generated)
8. [Reference configs: sysbench-style JSON files](#8-reference-configs-sysbench-style-json-files)
9. [OS tuning (high QPS with short connections)](#9-os-tuning-high-qps-with-short-connections)

---

## 1. Execution model: what it actually does

```
start `concurrency` workers (goroutines); each worker holds exactly 1 DB connection
each worker loops:
    run one "session":
        (if use_transaction) BEGIN
        run templates[0], templates[1], ... in order (each `repeat` times)
            params are freshly generated before every execution
        (if use_transaction) COMMIT; ROLLBACK on error
    wait for the next tick of rate_per_thread
until Ctrl-C (SIGINT) or SIGTERM
```

- **One session = one pass over all templates.** With `use_transaction` it is one transaction.
- Upper bound of total throughput ≈ `concurrency × rate_per_thread` sessions per second.
- If any statement in a session fails, the session is aborted (rolled back), one `ERROR` line is logged, and the worker moves on to the next session.

## 2. Quick start

### 2.1 Build

```bash
go build -o database_workload .
```

### 2.2 Load data with sysbench, then drive the load with this tool (recommended)

This tool only runs SQL; it does not create data. The easiest path is to let sysbench's own `prepare` create and fill the tables, then use this tool for whatever load shape you want.
sysbench creates `sbtest1..sbtestN` with `id INT AUTO_INCREMENT PRIMARY KEY, k INT, c CHAR(120), pad CHAR(60), KEY k_1(k)`, which is exactly what the reference configs in section 8 expect.

```bash
# 1) create 100 tables x 10000 rows (database `test` must already exist)
sysbench oltp_read_write \
  --mysql-host=127.0.0.1 --mysql-port=4000 --mysql-user=root --mysql-db=test \
  --tables=100 --table-size=10000 --threads=16 \
  prepare

# 2) run the load
./database_workload -config sysbench-config-oltp-read-write-fixprepared.json

# 3) stop with Ctrl-C
```

The reference configs' `db_conn_str` all point at a local playground, `root:@tcp(127.0.0.1:4000)/test` (root, empty password). **Replace it with your own address, user and password before use.**

**Important: the number ranges in the JSON must match the `prepare` options.**

| sysbench option | Where in the JSON | Value in the reference configs |
|---|---|---|
| `--tables=100` | `max` of the table-picking param (`"save_as": "tableNum"`) | `100` |
| `--table-size=10000` | `max` of every id param | `10000` |
| `--mysql-db=test` | database name in `db_conn_str` | `/test` |

If the ranges are larger than the real data, nothing errors — SELECTs just return no rows and UPDATEs touch 0 rows — but the load is no longer what you think it is.

Why not just `sysbench run`? sysbench only does long connections and only its own way of using prepared statements.
This tool can switch to short connections, the text protocol, multi-statement, pipelined binary, or a different hotspot distribution, against the same data, so you can compare them.

Not sure which settings match your production load? See [Extracting the workload features to simulate from TiDB Grafana](docs/extract-workload-from-grafana.md): it maps each setting to the Grafana panel that tells you what to set.

### 2.3 Non-sysbench loads: generate data with dbgen

For your own schemas (not sbtest), [kennytm/dbgen](https://github.com/kennytm/dbgen) is the recommended data generator.
It produces SQL dumps from a template with a random expression per column — a problem similar to this tool's param generation — so the two work well together:

- **dbgen decides what data is in the tables** (row count, value range and distribution of each column);
- **this tool decides how the load accesses that data** (which ids, where the hotspots are, how SQL is sent).

Keep the value ranges consistent on both sides: if dbgen generated rows with ids 1..1000000, set the id param's `min` / `max` to 1 and 1000000 here;
if a column only has a few enum values in dbgen, pick from the same values with `set` here. Otherwise most queries miss and the load no longer means much.

### 2.4 Command-line flags

Just one: `-config <path>`, default `config.json`.

---

## 3. Top-level settings, one by one

| Setting | Type | Default | Meaning |
|---|---|---|---|
| `db_conn_str` | string | (required) | A [go-sql-driver/mysql DSN](https://github.com/go-sql-driver/mysql#dsn-data-source-name), e.g. `root:@tcp(127.0.0.1:4000)/test?parseTime=true&timeout=3s&readTimeout=3s&writeTimeout=3s`. Include timeouts so a network problem doesn't hang workers. |
| `concurrency` | int | `0` | Number of workers. **Each worker uses exactly 1 connection**, so this is also the number of DB connections. |
| `rate_per_thread` | int | `0` | Sessions per second per worker (transactions per second when `use_transaction` is on). `0` or omitted = no limit, the next session starts right away. If one session takes longer than `1/rate` seconds, the next session starts right away (at most one tick is made up, since Go's ticker buffers only one), and the overall rate still falls below the setting. |
| `connection_type` | string | `"long"` | `"long"`: long-lived connection; `"short"`: open and close a connection for every session. See below. |
| `use_transaction` | bool | `false` | Wrap each session's statements in `BEGIN … COMMIT` (pessimistic by default on TiDB). |
| `use_prepared_statements` | bool | `false` | `true`: server-side prepared statements (binary protocol, `COM_STMT_PREPARE` / `COM_STMT_EXECUTE`); `false`: text protocol (`COM_QUERY`). |
| `fix_prepared_statement_reuse` | bool | `false` | Fixes "re-PREPARE and CLOSE in every transaction" so a statement is PREPAREd once per connection and EXECUTEd many times. See below. |
| `multi_statements` | bool | `false` | Send the whole transaction as **one** text-protocol request (one round trip). See below. |
| `multi_statements_mode` | string | `"prepared_cache"` | Only with `multi_statements: true`: `"prepared_cache"` or `"raw"`. |
| `pipelined_binary` | bool | `false` | Write BEGIN and all of the transaction's binary EXECUTE packets back to back, read the responses, then send COMMIT (two round trips). See below. |
| `templates` | array | (required) | SQL templates, see [section 6](#6-sql-templates). |

Unknown JSON keys are ignored, so you can add `"comment"` anywhere (the reference configs do).

### `connection_type`: long or short connections

- **`"long"` (default)**: each worker opens one connection and keeps reusing it; it only reconnects after an error.
  Use it to simulate:
  - **Apps with a connection pool**: Go (`database/sql`), Java (HikariCP, Druid, …), .NET (ADO.NET pooling), Node.js (`mysql2` pool),
    Python (SQLAlchemy pool), Ruby on Rails (ActiveRecord pool), Rust (`sqlx` pool).
  - **Benchmark tools like sysbench** that grab a connection at start and use it until the end.
  - Connection-level state (prepared statements, `scope: "connection"` variables) survives across transactions.
- **`"short"`**: **every session opens a new connection and closes it afterwards.**
  Use it to simulate:
  - **Apps without a connection pool**: PHP (PHP-FPM + mysqli / PDO without persistent connections) is the classic case;
  - Python Django (default `CONN_MAX_AGE=0` closes the connection at the end of each request);
  - Perl / Python CGI scripts, cron batch jobs, shell scripts calling the `mysql` CLI;
  - Serverless functions that don't reuse connections (e.g. an AWS Lambda that connects on every invocation).
  - Every transaction pays for a TCP handshake plus MySQL authentication; connection setup/teardown is itself the load.
  - Just like a real disconnect + connect, all client-side state tied to the connection is cleared at the start of every session: prepared statements are PREPAREd again and `scope: "connection"` variables are regenerated (e.g. a new table is picked).
  - Short connections burn through client ports fast; see [section 9](#9-os-tuning-high-qps-with-short-connections) for high QPS.

### `use_prepared_statements` and `fix_prepared_statement_reuse`

- `use_prepared_statements: false`: every statement goes out as text (`COM_QUERY`).
  - Note: **without** `interpolateParams=true` in the DSN, go-sql-driver silently turns a text query that has `?` args into PREPARE → EXECUTE → CLOSE.
    For true "one statement, one text request", add `interpolateParams=true` to the DSN (the client builds the final SQL).
- `use_prepared_statements: true`: server-side prepared statements, with one catch:
  - Together with `use_transaction`, Go's `database/sql` `Tx.StmtContext` **re-PREPAREs in every transaction and CLOSEs on commit**,
    so the cache does nothing and every transaction does PREPARE → EXECUTE → CLOSE. This happens to mimic plenty of Go apps that believe they reuse prepared statements.
  - Without a transaction this problem doesn't occur; statements are reused normally.
- `fix_prepared_statement_reuse: true`: works around the catch — BEGIN/COMMIT are sent as text without a `*sql.Tx`, and the cached statement is executed directly.
  Result: **each statement is PREPAREd once per connection and then only EXECUTEd**, like sysbench or Java with `cachePrepStmts=true`.
  This is usually the baseline for comparisons.

### `multi_statements` and `multi_statements_mode`

The whole transaction (BEGIN, every statement, COMMIT) is joined with `;` into **one** text-protocol request (a single `COM_QUERY`): one round trip.
Implemented with [`github.com/dulao5/tidb-multistmt`](https://github.com/dulao5/tidb-multistmt). When on, the tool appends `multiStatements=true` to the DSN for you.

- `"prepared_cache"` (default): uses SQL-level prepare inside the text protocol.
  The first time a statement appears on a connection it sends `PREPARE` (then caches it per physical connection, so it is correct with short connections too),
  and every execution sends `SET @p=…; EXECUTE stmt USING @p…`. In other words: text protocol, with SETs, but with the prepare optimization.
- `"raw"`: no prepare at all; argument values are inlined into the SQL text as literals and the whole transaction is sent as one `;`-joined string.
  The control group for "multi-statement without any prepared statements".

### `pipelined_binary`

**The binary-protocol version of multi-statement**, implemented with [`github.com/dulao5/tidb-binary-multistmt`](https://github.com/dulao5/tidb-binary-multistmt):
BEGIN and all of the transaction's `COM_STMT_EXECUTE` packets are written back to back as one batch, then all responses are read; if everything succeeded it sends COMMIT, otherwise ROLLBACK.
So each transaction takes **2 round trips** (one for the BEGIN + EXECUTE batch, one for COMMIT / ROLLBACK), with no `SET @…` text overhead.
Each statement is PREPAREd only once per connection (the first time it appears, before the batch is written).

- Pessimistic transactions only: if a statement in the middle fails, the statements already written still execute; the final ROLLBACK cleans up.
- When on, **`use_transaction` / `use_prepared_statements` / `multi_statements` / `fix_prepared_statement_reuse` / `connection_type` no longer matter**:
  it is always "transaction + binary prepared + long connection".

## 4. Execution modes at a glance (what goes over the wire per transaction)

For a transaction with N statements:

| Mode | Settings | Round trips per transaction | PREPAREs per transaction |
|---|---|---|---|
| Text, no transaction | `use_transaction:false`, `use_prepared_statements:false`, DSN with `interpolateParams=true` | N | 0 |
| Text + transaction | `use_transaction:true`, `use_prepared_statements:false`, DSN with `interpolateParams=true` | N + 2 | 0 |
| Text, DSN **without** `interpolateParams=true` (Go's default) | `use_prepared_statements:false` | 2N (+2 with a transaction): the driver turns every statement with args into PREPARE + EXECUTE, then CLOSE | N |
| Prepared (naive) | `use_transaction:true`, `use_prepared_statements:true` | 2N + 2 (PREPARE + EXECUTE per statement, CLOSE on commit) | N |
| **Prepared (fixed, baseline)** | the above plus `fix_prepared_statement_reuse:true` | N + 2 | first time only |
| Multi-statement | `use_transaction:true`, `use_prepared_statements:true`, `multi_statements:true` | 1 | first time only (text `PREPARE`) |
| Multi-statement raw | the above plus `multi_statements_mode:"raw"` | 1 | 0 |
| Pipelined binary | `pipelined_binary:true` | 2 (BEGIN + all EXECUTEs as one batch, then COMMIT or ROLLBACK) | first time only |

"First time only" assumes long connections; a short connection is new every session, so it has to PREPARE again each time.

### 4.1 Which settings match common languages / frameworks

The table maps each driver's **default behavior**. Real behavior depends on driver options and how the code is written, so check the "Condition" column against your app.
Set `use_transaction` according to the app code: `true` if it opens transactions explicitly, `false` if it relies on autocommit one statement at a time.
"Fixed prepared" in the table means with a transaction; for an app without transactions use `use_prepared_statements:true` + `use_transaction:false` instead (on a long connection that also PREPAREs only once per connection).

| Language / framework | Condition | What the driver actually does | Settings in this tool |
|---|---|---|---|
| Go (`database/sql` + go-sql-driver/mysql) | default: `db.Query(sql, args...)`, no `interpolateParams` in the DSN | PREPARE → EXECUTE → CLOSE for every statement with args | `long`, `use_prepared_statements:false`, DSN **without** `interpolateParams=true` |
| Go | DSN with `interpolateParams=true` | client builds the SQL, text protocol | `long`, `use_prepared_statements:false`, DSN with `interpolateParams=true` |
| Go | `PrepareContext` on a `*sql.Conn`, then `tx.StmtContext(stmt)` inside a transaction | re-PREPAREs in every transaction, CLOSEs on commit | `long`, `use_transaction:true`, `use_prepared_statements:true` (naive) |
| Go | `db.Prepare` (on the `*sql.DB`), then `tx.Stmt(stmt)` inside a transaction | PREPAREs once per connection, then reuses | roughly `long` + fixed prepared |
| Go (GORM) | `PrepareStmt: true` | caches prepared statements per connection | roughly `long` + fixed prepared |
| Java (JDBC, MySQL Connector/J) | default (`useServerPrepStmts=false`) | client builds the SQL, text protocol | `long`, text, DSN with `interpolateParams=true` |
| Java (JDBC) | `useServerPrepStmts=true&cachePrepStmts=true` | server-side prepared, cached per connection | `long` + fixed prepared |
| Java (JDBC) | `useServerPrepStmts=true` without `cachePrepStmts` | PREPARE on every `prepareStatement`, CLOSE on `close()` | `long` + naive prepared |
| PHP (PDO) | default (`PDO::ATTR_EMULATE_PREPARES=true`) | client-side emulated prepare, text protocol; new connection per request | `short`, text, DSN with `interpolateParams=true` |
| PHP (mysqli `prepare()`, or PDO with emulation off) | new connection per request | server-side prepared, PREPAREd again in every request | `short`, `use_prepared_statements:true` |
| Python (Django) | default `CONN_MAX_AGE=0`, driver mysqlclient / PyMySQL | client builds the SQL, text protocol; disconnects after each request | `short`, text, DSN with `interpolateParams=true` |
| Python (SQLAlchemy or anything with a pool) | driver mysqlclient / PyMySQL | client builds the SQL, text protocol | `long`, text, DSN with `interpolateParams=true` |
| Node.js (mysql2) | `query()` | client-side escaping, text protocol | `long`, text, DSN with `interpolateParams=true` |
| Node.js (mysql2) | `execute()` | server-side prepared, cached per connection | `long` + fixed prepared |
| Ruby on Rails (mysql2 / trilogy adapter) | default | client-side escaping, text protocol | `long`, text, DSN with `interpolateParams=true` |
| .NET (MySqlConnector) | default | client-side parameter substitution, text protocol | `long`, text, DSN with `interpolateParams=true` |
| .NET (MySqlConnector) | calling `MySqlCommand.Prepare()` | server-side prepared | `long` + fixed prepared |
| Rust (sqlx) | default | server-side prepared, cached per connection | `long` + fixed prepared |
| sysbench | default (`--db-ps-mode=auto`) | server-side prepared, PREPAREd once per connection | `long` + fixed prepared (what the `sysbench-config-oltp-*.json` files use) |

Multi-statement and pipelined binary aren't any framework's default; they are there to measure "how much faster would it be if the app merged a transaction into one or two round trips".

## 5. Combinations that don't work together

Everything below was confirmed by actually running it against TiDB v8.5.

### 5.1 Execution mode × connection type

The switches aren't independent (multi-statement needs transactions and prepared statements; pipelined overrides the others), so the valid configurations are really these 8 execution modes times long / short connections:

| Execution mode | Key settings | `long` | `short` |
|---|---|---|---|
| Text, no transaction | `use_transaction:false`, `use_prepared_statements:false` | ✅ | ✅ |
| Text + transaction | `use_transaction:true`, `use_prepared_statements:false` | ✅ | ✅ |
| Prepared, no transaction | `use_transaction:false`, `use_prepared_statements:true` | ✅ | ✅ |
| Prepared (naive) + transaction | `use_transaction:true`, `use_prepared_statements:true` | ✅ | ✅ |
| Prepared (fixed) + transaction | the above plus `fix_prepared_statement_reuse:true` | ✅ | ✅ |
| Multi-statement (`prepared_cache`) | `use_transaction`, `use_prepared_statements`, `multi_statements` all `true` | ✅ | ✅ |
| Multi-statement (`raw`) | the above plus `multi_statements_mode:"raw"` | ✅ | ✅ |
| Pipelined binary | `pipelined_binary:true` | ✅ | ⚠️ `short` has no effect, still a long connection |

Both text modes were tested with and without `interpolateParams=true` in the DSN, with the same result.

### 5.2 Every error and ignored setting

**Refused at startup:**

| Combination | Error |
|---|---|
| `multi_statements:true` without both `use_transaction` and `use_prepared_statements` set to `true` | `multi_statements requires use_transaction and use_prepared_statements to both be true` |
| `fix_prepared_statement_reuse:true` without both `use_transaction` and `use_prepared_statements`, or together with `multi_statements` | `fix_prepared_statement_reuse requires …` |
| `multi_statements_mode` other than `"prepared_cache"` / `"raw"` / empty | `multi_statements_mode must be …` |
| `pipelined_binary:true` with `multi_statements:true` but no `use_transaction` | same as the first row (the `multi_statements` check runs first) |

**Starts, but fails at run time:**

| Combination | What happens |
|---|---|
| A `?` in an identifier position (e.g. a table name) whose param lacks `"literal": true`, on a server-side-prepare path (`use_prepared_statements`, `multi_statements`, `pipelined_binary`, or text mode without `interpolateParams=true`) | `Error 1064 … syntax error`, because MySQL can't bind a table name. See [7.6](#76-literal-put-the-value-straight-into-the-sql-text). |
| `type:"ref"` pointing at a variable that hasn't been generated yet | `session variable "xxx" referenced before it was saved`; every session fails. |

**Runs, but some settings are silently ignored:**

| Combination | Ignored |
|---|---|
| `pipelined_binary:true` | `use_transaction`, `use_prepared_statements`, `multi_statements`, `multi_statements_mode`, `fix_prepared_statement_reuse`, `connection_type` (always long) |
| `multi_statements_mode` with `multi_statements:false` | `multi_statements_mode` |

For what works with short connections, see the table in 5.1.

## 6. SQL templates

```jsonc
{
  "sql": "SELECT c FROM sbtest? WHERE id = ?",
  "repeat": 10,
  "comment": "free text, ignored",
  "params": [ { …param for the 1st ?… }, { …param for the 2nd ?… } ]
}
```

| Field | Meaning |
|---|---|
| `sql` | SQL text with `?` placeholders. **The number of `?` must equal the number of `params`**; they are matched in order. |
| `params` | How each `?` value is generated, see section 7. |
| `repeat` | How many times this template runs in a row within a session, default 1. Params are regenerated each time. |
| empty `sql` | A "generate only, don't execute" template: params are generated as usual (typically to `save_as` a variable), but no SQL is sent. The reference configs use one to pick a table when the connection starts. |

**`IN (?)` is expanded automatically**: if a param generates an array (`type: "array"`), `IN (?)` becomes `IN (?, ?, ?, ?)` with one placeholder per element.

## 7. How params are generated

### 7.1 Lifetime: when a new value is generated

- **Default**: every param gets **a fresh random value on every execution**. `repeat: 10` means 10 independent values.
- **`save_as` + `scope: "transaction"`** (the default when `scope` is omitted): the value is stored as a variable that later params **in the same session (transaction)** can reuse via `ref`. Cleared when each new session starts.
  Example: DELETE a random id, then INSERT it back with the same id.
- **`save_as` + `scope: "connection"`**: generated **once per worker** and reused from then on.
  Example: like sysbench, each connection picks one random table at start and every transaction hits only that table.
  With `connection_type: "short"` every session is a new connection, so the value is generated again for every session.
- **`type: "ref"`**: generates nothing, only looks a value up — first in the session's variables, then in the connection-scope ones. The variable must have been generated **earlier** (an earlier template, or an earlier param of the same template).

### 7.2 Numbers (`type: "number"`)

| `random_mode` | Required | Distribution |
|---|---|---|
| `uniform` | `min`, `max` | Uniform over `[min, max]`, both ends inclusive. |
| `power_law` | `min`, `max`, `exponent` | Power law: **the closer to `min`, the hotter**. Larger `exponent` = more concentrated. `exponent` must not be `1.0`. |
| `partition_power_law` | `min`, `max`, `exponent`, `partition` | Split `[min, max]` into `partition` equal ranges, pick one uniformly, then draw a power-law value inside it. Result: **many scattered hotspots** (one at the start of each range). |

**Actual shapes** (200,000 samples each):

| Config | Median | p90 | Hottest values |
|---|---|---|---|
| `uniform` 1..10000 | 5001 | 8979 | ~0.01% each |
| `power_law` 1..1000000, `exponent: 2` | 2 | 10 | value 1 = 33%, values 1–5 ≈ 82% together |
| `power_law` 1..1000000, `exponent: 1.01` | 781 | 229088 | values 1–5 ≈ 13% together, long tail over the whole range |
| `power_law` 1..1000000, `exponent: 0.5` | 251153 | 809958 | no clear hotspot, skewed toward small values |
| `partition_power_law` 1..1000000, `exponent: 2`, `partition: 10` | — | — | 10 hotspots (first value of each range), ~3.4% each |

Caveats:

- The shape of `power_law` is determined by `max`; `min` only shifts it. The code draws a power-law value over `[1, max]`, adds `min - 1`, and clamps anything above `max` to `max`.
  So when `min` isn't 1, a small share of values piles up on `max`.
- With `partition_power_law` and `exponent` close to 1 (e.g. 1.001), the in-range power-law value often overshoots the range and is clamped to **the last value of the range**,
  so the hotspot moves from the start of each range to its end. For one clear hotspot at the start of each range, use `exponent` ≥ 1.5.
- `partition_power_law` with `exponent: 1.0` doesn't error; it falls back to uniform within the range.

### 7.3 Strings (`type: "string"`)

| `random_mode` | Required | Notes |
|---|---|---|
| `random_string` | `length` | Random string of `length` characters. Optional `charset`, default `a-zA-Z0-9`. E.g. sysbench's `c CHAR(120)` uses `length: 120`. |
| `number_format` | `format`, `number_config` | Generate an integer with `number_config` (a number param; `type` can be omitted), then format it with Go's `fmt.Sprintf(format, n)`. E.g. `"format": "user_%d"` → `user_42`; `"%08d"` → `00000042`. Hotspot distributions carry over from the number. |
| `set` + `set_mode: "uniform"` | `values` (array of strings) | Pick one with equal probability. E.g. `"values": ["paid", "unpaid", "canceled"]`. |
| `set` + `set_mode: "weighted"` | `values` (string → weight) | Pick by weight. Weights don't need to sum to 1. E.g. `"values": {"paid": 0.7, "unpaid": 0.2, "canceled": 0.1}`. |

### 7.4 Dates (`type: "date"`)

| `random_mode` | Required | Notes |
|---|---|---|
| `timestamp_range` | `start_time`, `end_time`, `format` | Pick a second uniformly in `[start_time, end_time)` (UTC) and format it as a string with `format`. |

- `start_time` / `end_time` are RFC 3339, e.g. `"2024-01-01T00:00:00Z"`.
- `format` is a Go time layout (written using the reference time "2006-01-02 15:04:05"):
  `"2006-01-02 15:04:05"` → `2024-03-07 18:22:41`, `"2006-01-02"` → `2024-03-07`.

```json
{ "type": "date", "random_mode": "timestamp_range",
  "start_time": "2024-01-01T00:00:00Z", "end_time": "2024-12-31T23:59:59Z",
  "format": "2006-01-02 15:04:05" }
```

(The old README's `"random_mode": "range"`, `"start"`, `"end"` were wrong; the code doesn't accept them.)

### 7.5 Arrays (`type: "array"`) — for `IN (?)`

| Field | Notes |
|---|---|
| `array_size` | Number of elements, must be > 0. |
| `element_type` | `number` / `string` / `date`. |
| `element_config` | How each element is generated, same shape as a param of that type (`type` can be omitted). Elements are independent and **may repeat**. |

```json
{
  "sql": "SELECT * FROM users WHERE id IN (?)",
  "params": [
    { "type": "array", "array_size": 4, "element_type": "number",
      "element_config": { "random_mode": "uniform", "min": 1, "max": 10000 } }
  ]
}
```

What is actually sent: `SELECT * FROM users WHERE id IN (?, ?, ?, ?)`.

### 7.6 `literal`: put the value straight into the SQL text

MySQL bind parameters can only stand where a value goes — **a table or column name can't be `?`**.
With `"literal": true`, the param's value is substituted into the SQL text before sending instead of being bound:

```json
{ "sql": "SELECT c FROM sbtest? WHERE id = ?",
  "params": [
    { "type": "ref", "ref_name": "tableNum", "literal": true },
    { "type": "number", "random_mode": "uniform", "min": 1, "max": 10000 }
  ] }
```

What gets PREPAREd is `SELECT c FROM sbtest37 WHERE id = ?`. Every distinct table name is a distinct prepared statement.

### 7.7 `offset`: add a fixed number to the value

Integers only (fresh or from `ref`). Typical use: the upper bound of a range query = lower bound + range size:

```json
{ "sql": "SELECT c FROM sbtest? WHERE id BETWEEN ? AND ?",
  "params": [
    { "type": "ref", "ref_name": "tableNum", "literal": true },
    { "type": "number", "random_mode": "uniform", "min": 1, "max": 10000, "save_as": "from" },
    { "type": "ref", "ref_name": "from", "offset": 100 }
  ] }
```

### 7.8 Param field reference

| Field | Used by | Notes |
|---|---|---|
| `type` | all | `number` / `string` / `date` / `array` / `ref` |
| `random_mode` | number / string / date | see above |
| `min`, `max` | number | integers, inclusive |
| `exponent` | number (power law) | float |
| `partition` | number (partitioned power law) | number of ranges |
| `length`, `charset` | string (`random_string`) | length; character set (optional) |
| `format` | string (`number_format`) / date | `fmt.Sprintf` format / Go time layout |
| `number_config` | string (`number_format`) | a number param |
| `set_mode`, `values` | string (`set`) | `uniform` + array, or `weighted` + object |
| `start_time`, `end_time` | date | RFC 3339 |
| `array_size`, `element_type`, `element_config` | array | see 7.5 |
| `save_as`, `scope` | all but ref | store as a variable; `transaction` (default) or `connection` |
| `ref_name` | ref | variable to look up |
| `offset` | integer values | add a fixed number |
| `literal` | all | `true` = substitute into the SQL text |

## 8. Reference configs: sysbench-style JSON files

All of them expect data from `sysbench … --tables=100 --table-size=10000 prepare` (if you change the table count or size, change the JSON too — see 2.2).
Their `db_conn_str` is the local `root:@tcp(127.0.0.1:4000)/test`; replace it with your own address, user and password.
They default to `concurrency: 40`, `rate_per_thread: 1` (i.e. 40 transactions/s); raise these for real load.
Unless noted, they use the baseline mode: long connection + transaction + fixed prepared.

### 8.1 By sysbench scenario

| File | sysbench equivalent | Per transaction |
|---|---|---|
| `sysbench-config-oltp-point-select.json` | `oltp_point_select` | 1 primary-key lookup, no transaction |
| `sysbench-config-oltp-read-only.json` | `oltp_read_only` | 10 point selects + 4 range queries (simple / SUM / ORDER BY / DISTINCT) |
| `sysbench-config-oltp-write-only.json` | `oltp_write_only` | indexed-column UPDATE + non-indexed UPDATE + DELETE then INSERT of the same id |
| `sysbench-config-oltp-read-write-fixprepared.json` | `oltp_read_write` | both of the above: 10 point selects + 4 ranges + 2 UPDATEs + DELETE + INSERT |
| `sysbench-config-oltp-update-index.json` | `oltp_update_index` | 1 `UPDATE … SET k = k + 1`, no transaction |
| `sysbench-config-oltp-update-non-index.json` | `oltp_update_non_index` | 1 `UPDATE … SET c = ?`, no transaction |
| `sysbench-config-oltp-insert.json` | `oltp_insert` | 1 INSERT (auto-increment id), no transaction |

All ids are `uniform` (unlike sysbench's default `--rand-type=special`). For hotspots, change the id params to `power_law` or `partition_power_law`.

### 8.2 The same oltp_read_write, different execution modes (for comparisons)

| File | Mode | Difference from the baseline |
|---|---|---|
| `sysbench-config-oltp-read-write-fixprepared.json` | **baseline**: fixed prepared | — |
| `sysbench-config-oltp-read-write.json` | naive prepared (PREPARE / CLOSE in every transaction) | no `fix_prepared_statement_reuse` |
| `sysbench-config-oltp-read-write-multi.json` | multi-statement (`prepared_cache`) | `multi_statements: true` |
| `sysbench-config-oltp-read-write-pipelined.json` | pipelined binary | `pipelined_binary: true` |

To test short connections (simulating PHP), set `connection_type` to `"short"` in any of these files (except `-pipelined.json`, where it has no effect).
Note that with short connections a new table is picked every session.

### 8.3 Early, small configs (for smoke tests)

| File | Contents |
|---|---|
| `sysbench-config.json` | one table per connection (1..40), 10 `WHERE id IN (?)` lookups per transaction; text protocol (DSN has `interpolateParams=true`) |
| `sysbench-config-prepared.json` | same, naive prepared |
| `sysbench-config-prepared-multi.json` | same, multi-statement |
| `config.json` | a few `IN (?)` array-param examples (on `sbtest1..3`), showing the `array` type |

These use 40 tables and ids up to 500000, unlike 8.1 / 8.2.

### 8.4 Full example: sysbench-config-oltp-read-write-fixprepared.json

```json
{
  "concurrency": 40,
  "db_conn_str": "root:@tcp(127.0.0.1:4000)/test?parseTime=true&timeout=3s&readTimeout=3s&writeTimeout=3s",
  "use_transaction": true,
  "use_prepared_statements": true,
  "multi_statements": false,
  "fix_prepared_statement_reuse": true,
  "rate_per_thread": 1,
  "connection_type": "long",
  "templates": [
    { "sql": "", "comment": "pick one table per connection (1..100) and keep it",
      "params": [ { "type": "number", "random_mode": "uniform", "min": 1, "max": 100, "save_as": "tableNum", "scope": "connection" } ] },
    { "sql": "SELECT c FROM sbtest? WHERE id = ?", "repeat": 10,
      "params": [ { "type": "ref", "ref_name": "tableNum", "literal": true },
                  { "type": "number", "random_mode": "uniform", "min": 1, "max": 10000 } ] },
    { "sql": "SELECT c FROM sbtest? WHERE id BETWEEN ? AND ?",
      "params": [ { "type": "ref", "ref_name": "tableNum", "literal": true },
                  { "type": "number", "random_mode": "uniform", "min": 1, "max": 10000, "save_as": "simpleRangeFrom" },
                  { "type": "ref", "ref_name": "simpleRangeFrom", "offset": 100 } ] },
    { "sql": "SELECT SUM(c) FROM sbtest? WHERE id BETWEEN ? AND ?",
      "params": [ { "type": "ref", "ref_name": "tableNum", "literal": true },
                  { "type": "number", "random_mode": "uniform", "min": 1, "max": 10000, "save_as": "sumRangeFrom" },
                  { "type": "ref", "ref_name": "sumRangeFrom", "offset": 100 } ] },
    { "sql": "SELECT c FROM sbtest? WHERE id BETWEEN ? AND ? ORDER BY c",
      "params": [ { "type": "ref", "ref_name": "tableNum", "literal": true },
                  { "type": "number", "random_mode": "uniform", "min": 1, "max": 10000, "save_as": "orderRangeFrom" },
                  { "type": "ref", "ref_name": "orderRangeFrom", "offset": 100 } ] },
    { "sql": "SELECT DISTINCT c FROM sbtest? WHERE id BETWEEN ? AND ?",
      "params": [ { "type": "ref", "ref_name": "tableNum", "literal": true },
                  { "type": "number", "random_mode": "uniform", "min": 1, "max": 10000, "save_as": "distinctRangeFrom" },
                  { "type": "ref", "ref_name": "distinctRangeFrom", "offset": 100 } ] },
    { "sql": "UPDATE sbtest? SET k = k + 1 WHERE id = ?",
      "params": [ { "type": "ref", "ref_name": "tableNum", "literal": true },
                  { "type": "number", "random_mode": "uniform", "min": 1, "max": 10000 } ] },
    { "sql": "UPDATE sbtest? SET c = ? WHERE id = ?",
      "params": [ { "type": "ref", "ref_name": "tableNum", "literal": true },
                  { "type": "string", "random_mode": "random_string", "length": 120 },
                  { "type": "number", "random_mode": "uniform", "min": 1, "max": 10000 } ] },
    { "sql": "DELETE FROM sbtest? WHERE id = ?",
      "params": [ { "type": "ref", "ref_name": "tableNum", "literal": true },
                  { "type": "number", "random_mode": "uniform", "min": 1, "max": 10000, "save_as": "delInsID" } ] },
    { "sql": "INSERT INTO sbtest? (id, k, c, pad) VALUES (?, ?, ?, ?)",
      "params": [ { "type": "ref", "ref_name": "tableNum", "literal": true },
                  { "type": "ref", "ref_name": "delInsID" },
                  { "type": "number", "random_mode": "uniform", "min": 1, "max": 10000 },
                  { "type": "string", "random_mode": "random_string", "length": 120 },
                  { "type": "string", "random_mode": "random_string", "length": 60 } ] }
  ]
}
```

(`comment` fields trimmed; otherwise identical to the file in the repo.)

## 9. OS tuning (high QPS with short connections)

With `connection_type: "short"` every transaction opens a new TCP connection, so client ports and TIME_WAIT quickly become the bottleneck. On the Linux machine running this tool:

```bash
sysctl -w net.ipv4.ip_local_port_range="1024 65535"
sysctl -w net.ipv4.tcp_tw_reuse=1
sysctl -w net.ipv4.tcp_fin_timeout=10
sysctl -w net.netfilter.nf_conntrack_max=1048576
sysctl -w net.netfilter.nf_conntrack_buckets=262144

ulimit -n 1048576
echo "* soft nofile 1048576" >> /etc/security/limits.conf
echo "* hard nofile 1048576" >> /etc/security/limits.conf

# append to /etc/systemd/system.conf and /etc/systemd/user.conf
DefaultLimitNOFILE=1048576
# restart systemd-logind
systemctl restart systemd-logind
```

In short-connection mode the tool also sets `SO_REUSEADDR` and `SO_LINGER=0` on its sockets (close sends RST, so the client keeps no TIME_WAIT); see `worker/dialer_base.go`.
