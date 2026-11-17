# Extracting the workload features to simulate from TiDB Grafana

English | [中文](extract-workload-from-grafana-zh.md) | [日本語](extract-workload-from-grafana-ja.md)

To reproduce a production load with this tool you first need to know **how the application talks to TiDB**.
Most of that is visible in the **TiDB** Grafana dashboard (the `{cluster}-TiDB` dashboard, rows **Server**, **Query Summary**, **Query Detail**, **Transaction**, **Executor**).
This page maps each setting of `database_workload` to the panel that tells you what to set.

Panel and metric names below are taken from `pkg/metrics/grafana/tidb.json` in pingcap/tidb (master). Older versions may use slightly different panel titles.
Always select the **time range of the real peak** and, if the cluster has several TiDB nodes, look at the panels **per instance** (a single node can look different from the sum).

| What you want to know | Setting(s) it decides | Section |
|---|---|---|
| Long or short connections | `connection_type` | [1](#1-long-or-short-connections) |
| Read / write mix and how much is written | the SQL in `templates`, `use_transaction` | [2](#2-readwrite-mix-and-written-rows) |
| Text protocol, or prepared statements (and whether they are reused) | `use_prepared_statements`, `fix_prepared_statement_reuse` | [3](#3-text-protocol-or-prepared-statements) |
| Multi-statement | `multi_statements`, `multi_statements_mode` | [4](#4-multi-statement) |
| Transactions and their size | `use_transaction`, number of statements per template | [5](#5-transactions) |
| How many connections and how fast | `concurrency`, `rate_per_thread` | [6](#6-concurrency-and-rate) |
| What Grafana can **not** tell you | params, hotspots, SQL text | [7](#7-what-grafana-cannot-tell-you) |

## 1. Long or short connections

Dashboard path: **TiDB → Server → Connection Count / Disconnection Count**, and **Query Summary → Command Per Second / CPS By Instance**.

1. **Connection Count**: is the number of connections per TiDB node *stable*?
   - A flat line (for example, always 200 connections) means a **connection pool** → `"connection_type": "long"`.
   - A line that jumps around, or stays far below the concurrency you expect, may mean connections are opened and closed all the time.
2. **Disconnection Count** is a *counter* (`tidb_server_disconnection_total`), and the panel plots it **without** `rate()`.
   Open the panel's query in Explore and wrap it: `sum(rate(tidb_server_disconnection_total[1m]))`.
   Compare that rate with the statement rate (**Query Summary → QPS → total**) or the transaction rate (**Transaction → Transaction OPS**):
   - disconnect rate **much smaller** than QPS → long connections;
   - disconnect rate **close to the transaction (or request) rate** → about one connection per transaction → `"short"`.
3. **Cross-check with the commands**: in **CPS By Instance** (`tidb_server_query_total`, labels `type` / `result`),
   a short-connection application sends a `Quit` command with every connection, so `Quit` is roughly as frequent as disconnects.
   Commands such as `InitDB` (the `Use` statement) and `Ping` are also frequent.
4. **Statement hints in QPS**: in **Query Summary → QPS** (`tidb_executor_statement_total`, grouped by `type`), per-connection bookkeeping shows up as `Use`, `Set`, and sometimes `Rollback`.
   Their rate has a clear relation to the connection (re)setup rate: a pool that resets the connection on borrow, or an app that connects for every request and runs `SET NAMES ...` / `USE db` first.
   This is only a hint, not proof: a long-connection app may also run `SET` for its own reasons.
5. **Connection reset**: some drivers/pools send *reset connection* or *change user*. These commands have no readable name in the panel;
   they appear as the **numeric** `type` labels `31` (reset connection) and `17` (change user) in **CPS By Instance**.
   A high rate means the pool resets the session state between uses (long connection, but session state is not kept).
   This tool has no setting for it; keep `"long"` and note the difference.

**Decision**: stable connection count + disconnect rate ≪ QPS → `long`. Disconnect rate ≈ transaction/request rate → `short`.

## 2. Read/write mix and written rows

Dashboard path: **Query Summary → QPS** and **Query Summary → Affected Rows By Type**.

- **QPS** shows the rate per statement `type` (`Select`, `Insert`, `Update`, `Delete`, `Begin`, `Commit`, `Rollback`, `Set`, `Use`, ...).
  Take the **average over the peak period** of each type and turn it into a percentage of the total: that is the SQL mix that your `templates` should reproduce
  (for example 80% `Select`, 15% `Update`, 5% `Insert`). Each template runs once per session; use `repeat`, or a different number of templates, to reach the ratio.
- **Affected Rows By Type** (`tidb_executor_affected_rows`, grouped by `sql_type`) shows **rows written per second** for INSERT/UPDATE/DELETE/REPLACE.
  Divide it by the statement rate of the same type to get *rows per statement* — that tells you whether an `UPDATE` touches 1 row (point update) or a range, and whether an `INSERT` is single-row or multi-row (`VALUES (...),(...)`, an array param).
- `Begin`/`Commit`/`Rollback` in QPS are not "work"; they tell you whether the app uses explicit transactions (section 5). Exclude them from the read/write ratio.

## 3. Text protocol or prepared statements

Dashboard path: **Query Summary → CPS By Instance**, **Server → Prepare Statement Count**, **Executor → Queries Using Plan Cache OPS**.

**CPS By Instance** (`tidb_server_query_total`) counts MySQL *commands*, not SQL statements. Look at the `type` and `result` labels:

| `type` (result OK) | Protocol meaning |
|---|---|
| `Query` | text protocol (`COM_QUERY`), including raw SQL, `PREPARE`/`EXECUTE` sent as SQL, multi-statement |
| `StmtPrepare` | `COM_STMT_PREPARE` (binary protocol) |
| `StmtExecute` | `COM_STMT_EXECUTE` |
| `StmtClose` | `COM_STMT_CLOSE` |

How to read the ratios:

- Almost only `Query` → text protocol → `"use_prepared_statements": false`.
- `StmtExecute` is the large majority, `StmtPrepare` and `StmtClose` are tiny → prepared statements are **reused** (a statement is prepared once per connection) → `"use_prepared_statements": true` plus `"fix_prepared_statement_reuse": true`.
- `StmtPrepare` ≈ `StmtExecute` (about 1:1, plus `StmtClose` close to them) → every execution is prepared and closed again (the "naive" pattern many drivers use by default) → `"use_prepared_statements": true` **without** `fix_prepared_statement_reuse`.
- A mix of both → some statements are prepared and some are not; set the setting for the dominant one, or run two workloads.
- Also look at **Server → Prepare Statement Count** (`tidb_server_prepared_stmts`): a small stable number is normal; a number that keeps growing means the client prepares without closing.
- **Executor → Queries Using Plan Cache OPS** shows how many statements hit the plan cache (prepared statements only, unless non-prepared plan cache is enabled).
- Failed commands (`result=Error`) belong in the same ratios; if errors are large, fix them first, the real application load may be different.

## 4. Multi-statement

Dashboard path: **TiDB → Query Detail → Queries In Multi-Statement**.

The panel shows the **number of queries contained in a multi-query request**: `avg` (queries per request) and `sum` (queries per second, from `tidb_server_multi_query_num`).

- `sum` is zero or the panel is empty → the application does not use multi-statement.
- `sum` > 0 → the application sends several `;`-separated statements in one `COM_QUERY`. `avg` is the batch size, which should roughly match the number of statements in your transaction template.
  Use `"multi_statements": true` (see the README for `multi_statements_mode`).
- Note that multi-statement requests are counted as `Query` in **CPS By Instance**, so a high `Query` share together with a non-zero multi-statement panel does *not* mean one command per statement.

## 5. Transactions

Dashboard path: **TiDB → Transaction → Transaction OPS / Transaction Statement Num / Duration**, and `Begin`/`Commit`/`Rollback` in **QPS**.

- **Transaction OPS** is the transaction rate. If `Begin`/`Commit` appear in QPS (and match Transaction OPS) the application uses explicit transactions → `"use_transaction": true`.
  If Transaction OPS is about equal to the DML rate and there is no `Begin`, the application is in autocommit mode with one statement per transaction → `"use_transaction": false`.
- **Transaction Statement Num** (histogram) shows how many statements a transaction contains. Use it to decide the number of statements per session (templates × `repeat`).
- **Duration** (P99/P95) shows how long a transaction lasts. If it is much longer than the sum of statement latencies, the application holds transactions open while it is busy elsewhere; this tool has no think-time setting, so mention the gap.
- `Rollback` in QPS: a high rollback rate may mean conflicts/retries or a pool that rolls back on release (see section 1).

## 6. Concurrency and rate

Dashboard path: **Server → Connection Count**, **Transaction OPS**, **Query Summary → Connection Idle Duration**.

- `concurrency` = number of workers = number of connections. Use the **total** line of **Connection Count** at the peak for long-connection apps.
  The **active connections** line (`tidb_server_tokens`) is the number actually executing; for a pool far larger than the real parallelism, it is a better figure.
  For short connections, *connection count is not concurrency* (connections come and go); estimate concurrency as `request rate × average latency` (Little's law) from **Transaction OPS** and **Duration**.
- `rate_per_thread` = (transaction rate per TiDB node) ÷ (`concurrency` simulated per node), in sessions per second per worker.
  `0` means "no limit": use it for a maximum-throughput test, not for a replay.
- **Connection Idle Duration** (`tidb_server_conn_idle_duration_seconds`, with `in_txn` 1/0) is how long a connection sits idle between commands:
  `in_txn=1` is the application's "think time" inside a transaction, `in_txn=0` between transactions.
  A large idle time means each worker is mostly waiting for the application; to keep a realistic load, use a lower `rate_per_thread` with more `concurrency`.

If the real cluster has several TiDB nodes, reproduce one node's share against one TiDB (or divide the numbers by the node count and point the tool at the load balancer).

## 7. What Grafana cannot tell you

These parts of the load are not in the Grafana dashboard; get them from elsewhere before filling `params`:

| Missing information | Where to look |
|---|---|
| The SQL text/templates and their share | `information_schema.statements_summary` (`cluster_statements_summary`), Top SQL, slow query log |
| Distribution of parameters (hot ids, ranges, `IN (...)` sizes) | slow query log / statement summary samples; for hotspots **TiDB Dashboard → Key Visualizer** and the **PD → Hot Region** panels |
| Result sizes | **Server → Client Data Traffic**, `Affected Rows`, slow log `Result_rows` |
| Data size and table layout | `information_schema.tables`, TiDB Dashboard → Cluster Info → Statistics |
| Isolation level, pessimistic/optimistic, autocommit | `SHOW VARIABLES`, session variables in connection strings |
| Network latency between the application and TiDB | measure from the application host; this tool runs wherever you start it |

Remember that Grafana gives **averages over a window** (the `rate()` window is 1 minute); bursts and daily peaks can be missed. Compare several peak windows before settling on numbers.
