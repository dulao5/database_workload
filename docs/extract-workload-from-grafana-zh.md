# 从 TiDB Grafana 提取需要模拟的负载特征

[English](extract-workload-from-grafana.md) | 中文 | [日本語](extract-workload-from-grafana-ja.md)

想用本工具复现生产负载，首先要知道**应用是怎么访问 TiDB 的**。
这些信息大部分都能在 **TiDB** Grafana 面板（`{cluster}-TiDB`，行 **Server**、**Query Summary**、**Query Detail**、**Transaction**、**Executor**）里看到。
本文把 `database_workload` 的每个设置项，对应到能告诉你该怎么设置的面板。

下面的面板名和指标名来自 pingcap/tidb master 的 `pkg/metrics/grafana/tidb.json`，旧版本的面板标题可能略有不同。
请始终选择**真实业务高峰的时间范围**；如果有多个 TiDB 节点，请**按 instance 分别看**（单个节点可能和总和不一样）。

| 想知道什么 | 决定的设置项 | 章节 |
|---|---|---|
| 长连接还是短连接 | `connection_type` | [1](#1-长连接还是短连接) |
| 读写比例和写入量 | `templates` 里的 SQL、`use_transaction` | [2](#2-读写比例和写入行数) |
| 文本协议还是 prepared statement（是否复用） | `use_prepared_statements`、`fix_prepared_statement_reuse` | [3](#3-文本协议还是-prepared-statement) |
| multi-statement | `multi_statements`、`multi_statements_mode` | [4](#4-multi-statement) |
| 事务及其大小 | `use_transaction`、每个模板的语句数 | [5](#5-事务) |
| 连接数和速率 | `concurrency`、`rate_per_thread` | [6](#6-并发和速率) |
| Grafana **看不到**的信息 | 参数、热点、SQL 文本 | [7](#7-grafana-看不到的信息) |

## 1. 长连接还是短连接

面板路径：**TiDB → Server → Connection Count / Disconnection Count**，以及 **Query Summary → Command Per Second / CPS By Instance**。

1. **Connection Count**：每个 TiDB 节点的连接数是否**稳定**？
   - 一条平稳的线（例如始终 200 个连接）说明用了**连接池** → `"connection_type": "long"`。
   - 连接数上蹿下跳，或远低于预期并发，可能是连接一直在建立、关闭。
2. **Disconnection Count** 是一个*计数器*（`tidb_server_disconnection_total`），面板上**没有**套 `rate()`。
   请在 Explore 里打开该面板的查询，自己包一层：`sum(rate(tidb_server_disconnection_total[1m]))`。
   再把这个速率和语句速率（**Query Summary → QPS → total**）或事务速率（**Transaction → Transaction OPS**）比较：
   - 断开速率**远小于** QPS → 长连接；
   - 断开速率**接近事务（或请求）速率** → 大约每个事务一个连接 → `"short"`。
3. **用命令类型交叉验证**：在 **CPS By Instance**（`tidb_server_query_total`，标签 `type` / `result`）里，
   短连接应用每个连接都会发一次 `Quit` 命令，所以 `Quit` 的频率和断开次数大致相当。
   `InitDB`（即 `Use` 语句）和 `Ping` 也会很频繁。
4. **QPS 里的线索**：在 **Query Summary → QPS**（`tidb_executor_statement_total`，按 `type` 分组）里，和连接相关的“杂务”会表现为 `Use`、`Set`，有时还有 `Rollback`。
   它们的速率和连接的建立/重置速率有明显相关性：连接池在借出连接时重置连接，或应用每个请求都新建连接并先执行 `SET NAMES ...` / `USE db`。
   这只是线索而不是证据：长连接应用也可能因其他原因执行 `SET`。
5. **重置连接**：有些驱动/连接池会发送 *reset connection* 或 *change user*。这些命令在面板里没有可读的名字，
   会在 **CPS By Instance** 里以**数字**形式的 `type` 标签出现：`31`（reset connection）和 `17`（change user）。
   速率很高说明连接池在两次使用之间重置会话状态（长连接，但不保留会话状态）。
   本工具没有对应设置；保持 `"long"` 并记下这个差异即可。

**判断**：连接数稳定且断开速率 ≪ QPS → `long`；断开速率 ≈ 事务/请求速率 → `short`。

## 2. 读写比例和写入行数

面板路径：**Query Summary → QPS** 和 **Query Summary → Affected Rows By Type**。

- **QPS** 按语句 `type`（`Select`、`Insert`、`Update`、`Delete`、`Begin`、`Commit`、`Rollback`、`Set`、`Use` 等）显示速率。
  取高峰期间每种类型的**平均值**，换算成占总量的百分比：这就是 `templates` 应该复现的 SQL 比例
  （例如 80% `Select`、15% `Update`、5% `Insert`）。每个模板每个会话执行一次，可以用 `repeat` 或不同数量的模板来凑出比例。
- **Affected Rows By Type**（`tidb_executor_affected_rows`，按 `sql_type` 分组）显示 INSERT/UPDATE/DELETE/REPLACE 每秒**写入的行数**。
  除以同类型的语句速率，就得到*每条语句影响的行数*——可以判断 `UPDATE` 是点更新还是范围更新，`INSERT` 是单行还是多行（`VALUES (...),(...)`、数组参数）。
- QPS 里的 `Begin`/`Commit`/`Rollback` 不是“业务”；它们说明应用是否使用显式事务（第 5 节）。计算读写比例时请排除它们。

## 3. 文本协议还是 prepared statement

面板路径：**Query Summary → CPS By Instance**、**Server → Prepare Statement Count**、**Executor → Queries Using Plan Cache OPS**。

**CPS By Instance**（`tidb_server_query_total`）统计的是 MySQL *命令*，不是 SQL 语句。看 `type` 和 `result` 标签：

| `type`（result 为 OK） | 协议含义 |
|---|---|
| `Query` | 文本协议（`COM_QUERY`），包括原生 SQL、以 SQL 形式发送的 `PREPARE`/`EXECUTE`、multi-statement |
| `StmtPrepare` | `COM_STMT_PREPARE`（二进制协议） |
| `StmtExecute` | `COM_STMT_EXECUTE` |
| `StmtClose` | `COM_STMT_CLOSE` |

比例的解读：

- 几乎只有 `Query` → 文本协议 → `"use_prepared_statements": false`。
- `StmtExecute` 占绝大多数，`StmtPrepare` 和 `StmtClose` 很少 → prepared statement 被**复用**（每个连接只 prepare 一次）→ `"use_prepared_statements": true` 加 `"fix_prepared_statement_reuse": true`。
- `StmtPrepare` ≈ `StmtExecute`（约 1:1，`StmtClose` 也接近）→ 每次执行都 prepare 再关闭（很多驱动默认的“朴素”用法）→ `"use_prepared_statements": true`，**不要**设 `fix_prepared_statement_reuse`。
- 两种混合 → 一部分语句用 prepare，一部分不用；按占比较大的一种设置，或者分别跑两个负载。
- 还要看 **Server → Prepare Statement Count**（`tidb_server_prepared_stmts`）：数量小且稳定是正常的；持续增长说明客户端只 prepare 不 close。
- **Executor → Queries Using Plan Cache OPS** 显示命中 plan cache 的语句数（默认只针对 prepared statement，除非开启了 non-prepared plan cache）。
- 失败的命令（`result=Error`）也属于同一个比例；如果错误很多，请先排查错误，真实业务负载可能和面板里看到的不同。

## 4. multi-statement

面板路径：**TiDB → Query Detail → Queries In Multi-Statement**。

该面板显示**一个 multi-query 请求里包含的查询数**：`avg`（每个请求的查询数）和 `sum`（每秒的查询数，来自 `tidb_server_multi_query_num`）。

- `sum` 为 0 或面板为空 → 应用没有使用 multi-statement。
- `sum` > 0 → 应用在一个 `COM_QUERY` 里发送多条以 `;` 分隔的语句。`avg` 就是批大小，应当和你事务模板里的语句数大致一致。
  使用 `"multi_statements": true`（`multi_statements_mode` 见 README）。
- 注意 multi-statement 请求在 **CPS By Instance** 里算作 `Query`，所以 `Query` 占比高同时 multi-statement 面板非零，**不**代表每条语句一个命令。

## 5. 事务

面板路径：**TiDB → Transaction → Transaction OPS / Transaction Statement Num / Duration**，以及 **QPS** 里的 `Begin`/`Commit`/`Rollback`。

- **Transaction OPS** 是事务速率。如果 QPS 里有 `Begin`/`Commit`（且和 Transaction OPS 对得上），说明应用使用显式事务 → `"use_transaction": true`。
  如果 Transaction OPS 约等于 DML 速率，且没有 `Begin`，说明应用是 autocommit 模式，每条语句一个事务 → `"use_transaction": false`。
- **Transaction Statement Num**（直方图）显示每个事务包含多少条语句。用它决定每个会话的语句数（模板数 × `repeat`）。
- **Duration**（P99/P95）显示事务持续多久。如果远大于各语句延迟之和，说明应用在事务打开期间忙于别的事；本工具没有 think time 设置，请记下这个差异。
- QPS 里的 `Rollback`：回滚率高可能意味着冲突/重试，也可能是连接池在归还连接时回滚（见第 1 节）。

## 6. 并发和速率

面板路径：**Server → Connection Count**、**Transaction OPS**、**Query Summary → Connection Idle Duration**。

- `concurrency` = worker 数 = 连接数。长连接应用取高峰时 **Connection Count** 的 **total** 线。
  **active connections** 线（`tidb_server_tokens`）是真正在执行的连接数；如果连接池远大于真实并行度，这个数更合适。
  短连接时，*连接数不等于并发*（连接不断出现又消失）；请用 **Transaction OPS** 和 **Duration** 按 `请求速率 × 平均延迟`（利特尔法则）估算并发。
- `rate_per_thread` =（每个 TiDB 节点的事务速率）÷（该节点模拟的 `concurrency`），单位是每个 worker 每秒的会话数。
  `0` 表示“不限速”：用于最大吞吐测试，不适合回放。
- **Connection Idle Duration**（`tidb_server_conn_idle_duration_seconds`，带 `in_txn` 1/0）是连接在两条命令之间空闲的时间：
  `in_txn=1` 是事务内应用的“think time”，`in_txn=0` 是事务之间。
  空闲时间很长说明每个 worker 大部分时间在等应用；为了保持真实的负载，请使用更低的 `rate_per_thread` 加更大的 `concurrency`。

如果真实集群有多个 TiDB 节点，请用一个 TiDB 来复现一个节点的份额（或把数字除以节点数，让工具连接负载均衡）。

## 7. Grafana 看不到的信息

负载的下面这些部分不在 Grafana 面板里；填写 `params` 之前请从别处获取：

| 缺少的信息 | 从哪里获取 |
|---|---|
| SQL 文本/模板及占比 | `information_schema.statements_summary`（`cluster_statements_summary`）、Top SQL、慢查询日志 |
| 参数分布（热点 id、范围、`IN (...)` 大小） | 慢查询日志 / statement summary 样本；热点看 **TiDB Dashboard → Key Visualizer** 和 **PD → Hot Region** 面板 |
| 结果集大小 | **Server → Client Data Traffic**、`Affected Rows`、慢日志 `Result_rows` |
| 数据量和表结构 | `information_schema.tables`、TiDB Dashboard → 集群信息 → 统计信息 |
| 隔离级别、悲观/乐观、autocommit | `SHOW VARIABLES`、连接串里的 session 变量 |
| 应用到 TiDB 的网络延迟 | 在应用所在主机测量；本工具在你启动它的机器上运行 |

请记住，Grafana 给出的是**时间窗口内的平均值**（`rate()` 窗口是 1 分钟）；突发流量和每天的峰值可能被漏掉。定下数字之前，请多对比几个高峰时间段。
