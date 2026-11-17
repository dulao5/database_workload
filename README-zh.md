# database_workload

[English](README.md) | 中文 | [日本語](README-ja.md)

**一个按 JSON 配置向 MySQL / TiDB 持续发送 SQL 的压测负载工具。**
你写好 SQL 模板和"参数怎么随机"，它就开 N 个并发、按指定速率一直跑，直到你按 Ctrl-C。

它能模拟的东西：

- **连接方式**：长连接（带连接池的应用）或每个事务新建连接（无连接池的应用）。
- **SQL 的发送方式**：普通文本查询、服务端 prepared statement、把整个事务打包成一两次往返发出去（multi-statement / pipelined binary）。
- **参数的分布**：均匀、幂律（热点）、分区幂律，以及"一个事务里多条 SQL 共用同一个随机 id""一个连接固定用一张表"之类的关联。

它**不做**的事：

- **不造数据。** 表和数据请先用 `sysbench prepare`（或别的工具）准备好，见[第 2 节](#2-快速开始)。
- **不输出 QPS / 延迟统计。** 日志里只有启动、停止和错误。吞吐和延迟请在数据库侧看（TiDB Dashboard、Grafana、`information_schema.statements_summary` 等）。

---

## 目录

1. [运行模型：它到底在做什么](#1-运行模型它到底在做什么)
2. [快速开始](#2-快速开始)
3. [顶层参数逐个说明](#3-顶层参数逐个说明)
4. [执行方式一览（每个事务在网络上发了什么）](#4-执行方式一览每个事务在网络上发了什么)
5. [不能一起用的组合](#5-不能一起用的组合)
6. [SQL 模板（templates）](#6-sql-模板templates)
7. [参数（params）是怎么生成的](#7-参数params是怎么生成的)
8. [参考配置：各种 sysbench JSON](#8-参考配置各种-sysbench-json)
9. [OS 调优（短连接高 QPS 时）](#9-os-调优短连接高-qps-时)

---

## 1. 运行模型：它到底在做什么

```
启动 concurrency 个 worker（goroutine），每个 worker 只持有 1 条数据库连接
每个 worker 循环：
    执行一次 "session"：
        （use_transaction 时）BEGIN
        按顺序执行 templates[0], templates[1], ...（每个模板执行 repeat 次）
            每次执行前，按 params 重新生成参数
        （use_transaction 时）COMMIT；出错则 ROLLBACK
    按 rate_per_thread 等待到下一个节拍
直到收到 Ctrl-C（SIGINT）或 SIGTERM
```

- **一个 session = 把所有模板从头到尾跑一遍。** 开了 `use_transaction` 就是一个事务。
- 总吞吐的上限 ≈ `concurrency × rate_per_thread` 个 session/秒。
- 一个 session 中任何一条 SQL 出错，这个 session 就中止（回滚），记一行 `ERROR` 日志，然后进入下一个 session。

## 2. 快速开始

### 2.1 编译

```bash
go build -o database_workload .
```

### 2.2 先用 sysbench 造数据，再用本工具跑负载（推荐做法）

本工具只跑 SQL，不造数据。最省事的办法是用 sysbench 自带的 `prepare` 建表、灌数据，再用本工具跑你想要的负载形状。
sysbench 建的表是 `sbtest1..sbtestN`，结构为 `id INT AUTO_INCREMENT PRIMARY KEY, k INT, c CHAR(120), pad CHAR(60), KEY k_1(k)`，正好和第 8 节的参考配置对得上。

```bash
# 1) 建 100 张表，每张 10000 行（数据库 test 需事先存在）
sysbench oltp_read_write \
  --mysql-host=127.0.0.1 --mysql-port=4000 --mysql-user=root --mysql-db=test \
  --tables=100 --table-size=10000 --threads=16 \
  prepare

# 2) 用本工具跑负载
./database_workload -config sysbench-config-oltp-read-write-fixprepared.json

# 3) Ctrl-C 停止
```

**关键：JSON 里的数字范围要和 `prepare` 的参数对上。**

| sysbench 参数 | 对应 JSON 位置 | 参考配置里的值 |
|---|---|---|
| `--tables=100` | 选表的那个参数：`"save_as": "tableNum"` 的 `max` | `100` |
| `--table-size=10000` | 所有 id 参数的 `max` | `10000` |
| `--mysql-db=test` | `db_conn_str` 里的库名 | `/test` |

范围比实际数据大，只会查不到行（SELECT 空结果、UPDATE 0 行），不会报错，但负载就失真了。

为什么不直接用 `sysbench run`？因为 sysbench 只能长连接、只能用它自己的 prepared 方式发 SQL；
而本工具可以换成短连接、文本协议、multi-statement、pipelined binary，或者换一种热点分布，用同一份数据做对比。

### 2.3 命令行参数

只有一个：`-config <path>`，默认 `config.json`。

---

## 3. 顶层参数逐个说明

| 参数 | 类型 | 默认 | 含义 |
|---|---|---|---|
| `db_conn_str` | string | （必填） | [go-sql-driver/mysql 的 DSN](https://github.com/go-sql-driver/mysql#dsn-data-source-name)，如 `root:@tcp(127.0.0.1:4000)/test?parseTime=true&timeout=3s&readTimeout=3s&writeTimeout=3s`。建议带上超时，避免网络故障时 worker 卡死。 |
| `concurrency` | int | `0` | 并发 worker 数。**每个 worker 恰好占 1 条连接**，所以这也是到数据库的连接数。 |
| `rate_per_thread` | int | `0` | 每个 worker 每秒执行几个 session（开事务时就是几个事务）。`0` 或不写 = 不限速，跑完一个立刻跑下一个。如果一个 session 本身耗时超过 `1/rate` 秒，实际速率会低于设定值（不会补发）。 |
| `connection_type` | string | `"long"` | `"long"`：长连接；`"short"`：每个 session 新建并关闭连接。详见下文。 |
| `use_transaction` | bool | `false` | 是否把一个 session 里的所有 SQL 包在 `BEGIN … COMMIT` 里（TiDB 默认是悲观事务）。 |
| `use_prepared_statements` | bool | `false` | `true`：走服务端 prepared statement（二进制协议 `COM_STMT_PREPARE` / `COM_STMT_EXECUTE`）；`false`：走文本协议 `COM_QUERY`。 |
| `fix_prepared_statement_reuse` | bool | `false` | 修正"每个事务都重新 PREPARE 再 CLOSE"的问题，让一条语句在一个连接上只 PREPARE 一次、之后反复 EXECUTE。详见下文。 |
| `multi_statements` | bool | `false` | 把整个事务拼成**一次**文本协议请求发出去（一次网络往返）。详见下文。 |
| `multi_statements_mode` | string | `"prepared_cache"` | 只在 `multi_statements: true` 时有意义：`"prepared_cache"` 或 `"raw"`。 |
| `pipelined_binary` | bool | `false` | 用二进制协议把 BEGIN 和整个事务的 EXECUTE 包连续写出去、读回结果后再发 COMMIT（两次往返）。详见下文。 |
| `templates` | array | （必填） | SQL 模板列表，见[第 6 节](#6-sql-模板templates)。 |

JSON 里不认识的字段会被忽略，所以可以随意写 `"comment"` 做注释（参考配置里就是这么用的）。

### `connection_type`：长连接还是短连接

- **`"long"`（默认）**：每个 worker 建一条连接，一直复用；只有出错时才断开重连。
  适合模拟：
  - **带连接池的应用**：Go（`database/sql`）、Java（HikariCP、Druid 等）、.NET（ADO.NET 连接池）、Node.js（`mysql2` 的 pool）、
    Python（SQLAlchemy 连接池）、Ruby on Rails（ActiveRecord 连接池）、Rust（`sqlx` 的 pool）。
  - **sysbench 这类压测工具**：开局拿到连接后一直用到结束。
  - 连接级的状态（prepared statement、`scope: "connection"` 的变量）都会跨事务保留。
- **`"short"`**：**每个 session 都新建一条连接，跑完就关掉。**
  适合模拟：
  - **没有连接池的应用**：PHP（PHP-FPM + mysqli / PDO，且没开持久连接），这是最典型的；
  - Python Django（默认 `CONN_MAX_AGE=0`，每个请求结束就断开）；
  - Perl / Python 的 CGI 脚本、cron 批处理、shell 里调用 `mysql` 命令行；
  - 不复用连接的 Serverless 函数（如每次调用都建连的 AWS Lambda）。
  - 这种负载的特点是：每个事务都要付出 TCP 握手 + MySQL 认证的代价，连接建立/销毁本身就是压力来源。
  - 短连接下源端口用得很快，高 QPS 时请看[第 9 节](#9-os-调优短连接高-qps-时)。

### `use_prepared_statements` 与 `fix_prepared_statement_reuse`

- `use_prepared_statements: false`：每条 SQL 都以文本发出（`COM_QUERY`）。
  - 注意：go-sql-driver 在**没有** `interpolateParams=true` 时，带 `?` 参数的文本查询会在内部偷偷做 PREPARE → EXECUTE → CLOSE。
    想要真正的"一条 SQL 一次文本请求"，请在 DSN 里加 `interpolateParams=true`（客户端拼好 SQL 再发）。
- `use_prepared_statements: true`：服务端 prepared statement。但这里有个坑：
  - 同时开了 `use_transaction` 时，Go 标准库 `database/sql` 的 `Tx.StmtContext` 会**每个事务都重新 PREPARE，提交时再 CLOSE**。
    也就是说缓存形同虚设，每个事务都是 PREPARE → EXECUTE → CLOSE。这其实正好模拟了很多"自以为在复用 prepared statement"的 Go 应用。
  - 不开事务时没有这个问题，语句会被正常复用。
- `fix_prepared_statement_reuse: true`：绕开上面的坑——BEGIN/COMMIT 以文本发送，不经过 `*sql.Tx`，
  缓存的语句直接执行。效果：**每条语句在一个连接上只 PREPARE 一次，之后一直 EXECUTE**，和 sysbench、Java 的 `cachePrepStmts=true` 行为一致。
  对比测试时通常以它为基线。

### `multi_statements` 与 `multi_statements_mode`

把整个事务（BEGIN、所有语句、COMMIT）拼成**一个**文本协议请求（一个 `COM_QUERY`，多条语句用 `;` 连接）发出去，一次网络往返。
用 [`github.com/dulao5/tidb-multistmt`](https://github.com/dulao5/tidb-multistmt) 实现。开启后工具会自动在 DSN 后追加 `multiStatements=true`。

- `"prepared_cache"`（默认）：在文本协议里使用 SQL 级的 prepare：
  一条语句在这个连接上第一次出现时发 `PREPARE`（之后缓存复用，按物理连接记录，所以短连接下也正确），
  每次执行发 `SET @参数=…; EXECUTE 语句 USING @参数…`。即"文本协议、带 SET，但有 prepare 优化"。
- `"raw"`：不用 prepare，参数值直接以字面量拼进 SQL 文本，整个事务一串 `;` 拼起来发出去。
  作为"multi-statement 但完全不用 prepared statement"的对照组。

### `pipelined_binary`

**二进制协议版的 multi-statement。** 用 [`github.com/dulao5/tidb-binary-multistmt`](https://github.com/dulao5/tidb-binary-multistmt) 实现：
把 BEGIN 和整个事务的 `COM_STMT_EXECUTE` 包作为同一批一口气连续写出去，再依次读回所有结果；全部成功就再发 COMMIT，有失败就发 ROLLBACK。
所以每个事务是 **2 次网络往返**（一批 BEGIN + EXECUTE，再一次 COMMIT / ROLLBACK），没有 `SET @…` 文本开销。
语句在连接上只 PREPARE 一次（第一次遇到时，在写出这一批之前先 PREPARE）。

- 只支持悲观事务：中途某条语句失败时，已经写出去的后续语句仍会被执行，靠最后整体 ROLLBACK 兜底。
- 开启后 **`use_transaction` / `use_prepared_statements` / `multi_statements` / `fix_prepared_statement_reuse` / `connection_type` 都不再起作用**：
  总是"事务 + 二进制 prepared + 长连接"。

## 4. 执行方式一览（每个事务在网络上发了什么）

假设一个事务里有 N 条语句：

| 方式 | 配置 | 每个事务的网络往返 | 每个事务的 PREPARE |
|---|---|---|---|
| 文本，无事务 | `use_transaction:false`，`use_prepared_statements:false`，DSN 加 `interpolateParams=true` | N | 0 |
| 文本 + 事务 | `use_transaction:true`，`use_prepared_statements:false`，DSN 加 `interpolateParams=true` | N + 2 | 0 |
| 文本，DSN **没有** `interpolateParams=true`（Go 的默认） | `use_prepared_statements:false` | 2N（开事务再 +2）：驱动对每条带参数的语句自动 PREPARE + EXECUTE，然后 CLOSE | N |
| prepared（朴素版） | `use_transaction:true`，`use_prepared_statements:true` | 2N + 2（每条都 PREPARE + EXECUTE，提交时再 CLOSE） | N |
| **prepared（修正版，基线）** | 上面再加 `fix_prepared_statement_reuse:true` | N + 2 | 仅第一次 |
| multi-statement | `use_transaction:true`，`use_prepared_statements:true`，`multi_statements:true` | 1 | 仅第一次（文本 `PREPARE`） |
| multi-statement raw | 上面再加 `multi_statements_mode:"raw"` | 1 | 0 |
| pipelined binary | `pipelined_binary:true` | 2（BEGIN + 全部 EXECUTE 一批写出，然后 COMMIT 或 ROLLBACK） | 仅第一次 |

"仅第一次"指的是长连接下；短连接每个 session 都是新连接，自然每次都要重新 PREPARE。

### 4.1 常见语言 / 框架对应哪种配置

下表按各驱动的**默认行为**对应。实际行为取决于驱动参数和代码写法，表里的"条件"列请对照你的应用确认。
另外，`use_transaction` 要看应用代码是否显式开事务：开了就设 `true`，靠 autocommit 一条一条执行就设 `false`。
表中的"修正版 prepared"指开事务的情况；应用不开事务时，请改用 `use_prepared_statements:true` + `use_transaction:false`（长连接下同样是每个连接只 PREPARE 一次）。

| 语言 / 框架 | 条件 | 驱动实际做的事 | 本工具的配置 |
|---|---|---|---|
| Go（`database/sql` + go-sql-driver/mysql） | 默认：`db.Query(sql, args...)`，DSN 没有 `interpolateParams` | 每条带参数的语句都 PREPARE → EXECUTE → CLOSE | `long`，`use_prepared_statements:false`，DSN **不加** `interpolateParams=true` |
| Go | DSN 加 `interpolateParams=true` | 客户端拼好 SQL，文本协议 | `long`，`use_prepared_statements:false`，DSN 加 `interpolateParams=true` |
| Go | 在 `*sql.Conn` 上 `PrepareContext`，再在事务里用 `tx.StmtContext(stmt)` | 每个事务都重新 PREPARE，提交时 CLOSE | `long`，`use_transaction:true`，`use_prepared_statements:true`（朴素版） |
| Go | `db.Prepare`（在 `*sql.DB` 上），再在事务里用 `tx.Stmt(stmt)` | 每个连接 PREPARE 一次后复用 | 近似 `long` + 修正版 prepared |
| Go（GORM） | `PrepareStmt: true` | 每个连接缓存 prepared statement | 近似 `long` + 修正版 prepared |
| Java（JDBC，MySQL Connector/J） | 默认（`useServerPrepStmts=false`） | 客户端拼 SQL，文本协议 | `long`，文本，DSN 加 `interpolateParams=true` |
| Java（JDBC） | `useServerPrepStmts=true&cachePrepStmts=true` | 服务端 prepared，每个连接缓存 | `long` + 修正版 prepared |
| Java（JDBC） | `useServerPrepStmts=true`，不开 `cachePrepStmts` | 每次 `prepareStatement` 都 PREPARE，`close()` 时 CLOSE | `long` + 朴素版 prepared |
| PHP（PDO） | 默认（`PDO::ATTR_EMULATE_PREPARES=true`） | 客户端模拟 prepare，文本协议；每个请求新建连接 | `short`，文本，DSN 加 `interpolateParams=true` |
| PHP（mysqli `prepare()`，或 PDO 关闭模拟） | 每个请求新建连接 | 服务端 prepared，每个请求都重新 PREPARE | `short` + 朴素版 prepared（需 `use_transaction:true`；短连接 + prepared + 不开事务目前跑不了，见第 5 节） |
| Python（Django） | 默认 `CONN_MAX_AGE=0`，驱动 mysqlclient / PyMySQL | 客户端拼 SQL，文本协议；每个请求结束就断开 | `short`，文本，DSN 加 `interpolateParams=true` |
| Python（SQLAlchemy 等带连接池的） | 驱动 mysqlclient / PyMySQL | 客户端拼 SQL，文本协议 | `long`，文本，DSN 加 `interpolateParams=true` |
| Node.js（mysql2） | `query()` | 客户端转义，文本协议 | `long`，文本，DSN 加 `interpolateParams=true` |
| Node.js（mysql2） | `execute()` | 服务端 prepared，每个连接缓存 | `long` + 修正版 prepared |
| Ruby on Rails（mysql2 / trilogy 适配器） | 默认 | 客户端转义，文本协议 | `long`，文本，DSN 加 `interpolateParams=true` |
| .NET（MySqlConnector） | 默认 | 客户端替换参数，文本协议 | `long`，文本，DSN 加 `interpolateParams=true` |
| .NET（MySqlConnector） | 调用 `MySqlCommand.Prepare()` | 服务端 prepared | `long` + 修正版 prepared |
| Rust（sqlx） | 默认 | 服务端 prepared，每个连接缓存 | `long` + 修正版 prepared |
| sysbench | 默认（`--db-ps-mode=auto`） | 服务端 prepared，每个连接 PREPARE 一次 | `long` + 修正版 prepared（`sysbench-config-oltp-*.json` 用的就是这种） |

multi-statement 和 pipelined binary 不对应某个现成框架的默认行为，它们用来评估"如果应用把一个事务合并成一两次往返，能快多少"。

## 5. 不能一起用的组合

以下结论都在 TiDB v8.5 上实际跑过确认。

### 5.1 执行方式 × 连接方式

这几个开关并不互相独立（multi-statement 依赖事务和 prepared，pipelined 会覆盖其他开关），所以合法的配置其实就是下面 8 种执行方式，再乘上长 / 短两种连接：

| 执行方式 | 关键配置 | `long` | `short` |
|---|---|---|---|
| 文本，无事务 | `use_transaction:false`，`use_prepared_statements:false` | ✅ | ✅ |
| 文本 + 事务 | `use_transaction:true`，`use_prepared_statements:false` | ✅ | ✅ |
| prepared，无事务 | `use_transaction:false`，`use_prepared_statements:true` | ✅ | ❌ 第二次起每次失败 |
| prepared（朴素版）+ 事务 | `use_transaction:true`，`use_prepared_statements:true` | ✅ | ✅ |
| prepared（修正版）+ 事务 | 上面再加 `fix_prepared_statement_reuse:true` | ✅ | ❌ 第二次起每次失败 |
| multi-statement（`prepared_cache`） | `use_transaction`、`use_prepared_statements`、`multi_statements` 都为 `true` | ✅ | ✅ |
| multi-statement（`raw`） | 上面再加 `multi_statements_mode:"raw"` | ✅ | ✅ |
| pipelined binary | `pipelined_binary:true` | ✅ | ⚠️ `short` 不生效，仍是长连接 |

两种文本方式都分别测了 DSN 有 / 没有 `interpolateParams=true`，结果相同。

### 5.2 各种出错和被忽略的情况

**启动时直接报错退出：**

| 组合 | 报错 |
|---|---|
| `multi_statements:true` 但 `use_transaction` 或 `use_prepared_statements` 不是 `true` | `multi_statements requires use_transaction and use_prepared_statements to both be true` |
| `fix_prepared_statement_reuse:true` 但 `use_transaction` / `use_prepared_statements` 不是 `true`，或者同时开了 `multi_statements` | `fix_prepared_statement_reuse requires …` |
| `multi_statements_mode` 不是 `"prepared_cache"` / `"raw"` / 空 | `multi_statements_mode must be …` |
| `pipelined_binary:true` 且 `multi_statements:true`，但没开 `use_transaction` | 同第一行（`multi_statements` 的检查先做） |

**能启动，但运行时出错：**

| 组合 | 现象 |
|---|---|
| `connection_type:"short"` + 服务端 prepared，且不经过 Go 的 `*sql.Tx`：即 `fix_prepared_statement_reuse:true`，或 `use_prepared_statements:true` 但 `use_transaction:false` | 第一个 session 成功，**之后每个都失败**：`sql: connection is already closed`（缓存的语句还绑在已关闭的旧连接上）。短连接下要用 prepared，请用"朴素版 + 事务"或 multi-statement。 |
| 表名等标识符位置用了 `?`，但参数没有 `"literal": true`，且走的是服务端 prepare（`use_prepared_statements`、`multi_statements`、`pipelined_binary`，或文本模式但 DSN 没有 `interpolateParams=true`） | `Error 1064 … syntax error`，因为 MySQL 不能把表名作为绑定参数。见 [7.6 节](#76-literal把值直接拼进-sql-文本)。 |
| `type:"ref"` 引用了一个还没生成过的变量 | `session variable "xxx" referenced before it was saved`，每个 session 都失败。 |

**能跑，但某些参数被悄悄忽略：**

| 组合 | 被忽略的 |
|---|---|
| `pipelined_binary:true` | `use_transaction`、`use_prepared_statements`、`multi_statements`、`multi_statements_mode`、`fix_prepared_statement_reuse`、`connection_type`（总是长连接） |
| `multi_statements_mode` 而 `multi_statements:false` | `multi_statements_mode` |

短连接下哪些能用，见 5.1 的表。

## 6. SQL 模板（templates）

```jsonc
{
  "sql": "SELECT c FROM sbtest? WHERE id = ?",
  "repeat": 10,
  "comment": "随便写，会被忽略",
  "params": [ { …第 1 个 ? 的参数… }, { …第 2 个 ? 的参数… } ]
}
```

| 字段 | 含义 |
|---|---|
| `sql` | SQL 文本，用 `?` 占位。**`?` 的个数必须和 `params` 的个数一致**，按顺序一一对应。 |
| `params` | 每个 `?` 的值怎么生成，见第 7 节。 |
| `repeat` | 这个模板在一个 session 里连续执行几次，默认 1。每次都重新生成参数。 |
| `sql` 为空字符串 | "只生成、不执行"的模板：参数照常生成（通常是为了 `save_as` 存一个变量），但不发任何 SQL。参考配置用它在连接开始时选一张表。 |

**`IN (?)` 会自动展开**：如果某个参数生成的是数组（`type: "array"`），`IN (?)` 会被展开成 `IN (?, ?, ?, ?)`，每个元素一个占位符。

## 7. 参数（params）是怎么生成的

### 7.1 生命周期：什么时候生成新值

- **默认**：每个参数**每次执行都重新随机**。`repeat: 10` 就是 10 次独立的随机值。
- **`save_as` + `scope: "transaction"`**（`scope` 不写时的默认值）：生成后存成一个变量，**这个 session（事务）内**后面的参数可以用 `ref` 引用同一个值。每个新 session 开始时清空。
  例：DELETE 一个随机 id，再用同一个 id INSERT 回去。
- **`save_as` + `scope: "connection"`**：**每个 worker 只生成一次**，之后一直用这个值。
  例：像 sysbench 一样，每个连接开局选一张随机表，之后所有事务都只打这张表。
  注意：它绑定的是 worker，不是物理连接。短连接下换了新连接，值也不变。
- **`type: "ref"`**：不生成，只引用。先查本 session 的变量，再查 connection 级的变量。被引用的变量必须在**更早的位置**（更前面的模板，或同一模板里更靠前的参数）生成过。

### 7.2 数字（`type: "number"`）

| `random_mode` | 必填字段 | 分布 |
|---|---|---|
| `uniform` | `min`, `max` | `[min, max]` 均匀分布，两端都包含。 |
| `power_law` | `min`, `max`, `exponent` | 幂律分布：**越靠近 `min` 越热**。`exponent` 越大越集中。`exponent` 不能是 `1.0`。 |
| `partition_power_law` | `min`, `max`, `exponent`, `partition` | 先把 `[min, max]` 均分成 `partition` 段，均匀地挑一段，再在段内做幂律。效果是**有很多个分散的热点**（每段开头一个）。 |

**实际分布形状**（各采样 20 万次实测）：

| 配置 | 中位数 | 90 分位 | 最热的值占比 |
|---|---|---|---|
| `uniform` 1..10000 | 5001 | 8979 | 每个值约 0.01% |
| `power_law` 1..1000000, `exponent: 2` | 2 | 10 | 值 1 占 33%，1~5 合计约 82% |
| `power_law` 1..1000000, `exponent: 1.01` | 781 | 229088 | 值 1~5 合计约 13%，长尾覆盖全范围 |
| `power_law` 1..1000000, `exponent: 0.5` | 251153 | 809958 | 无明显热点，偏向小值 |
| `partition_power_law` 1..1000000, `exponent: 2`, `partition: 10` | — | — | 10 个热点（每段第一个值），各约 3.4% |

使用上的注意：

- `power_law` 的形状由 `max` 决定，`min` 只是整体平移：实际算法在 `[1, max]` 上取幂律值再加 `min - 1`，超出 `max` 的截成 `max`。
  所以 `min` 不是 1 时，会有一小部分值堆在 `max` 上。
- `partition_power_law` 的 `exponent` 接近 1（如 1.001）时，段内的幂律值经常超出段的长度，被截到**段的最后一个值**，
  热点就从段头变成了段尾。想要"每段开头一个明显热点"，请用 `exponent` ≥ 1.5。
- `partition_power_law` 的 `exponent` 写 `1.0` 不会报错，段内会退化成均匀分布。

### 7.3 字符串（`type: "string"`）

| `random_mode` | 必填字段 | 说明 |
|---|---|---|
| `random_string` | `length` | 生成 `length` 个字符的随机串。字符集 `charset` 可选，默认 `a-zA-Z0-9`。例：sysbench 的 `c CHAR(120)` 用 `length: 120`。 |
| `number_format` | `format`, `number_config` | 先按 `number_config`（就是一个数字参数，`type` 可省略）生成一个整数，再用 Go 的 `fmt.Sprintf(format, n)` 格式化。例：`"format": "user_%d"` → `user_42`；`"%08d"` → `00000042`。热点分布可以从数字那边带过来。 |
| `set` + `set_mode: "uniform"` | `values`（字符串数组） | 从数组里等概率挑一个。例：`"values": ["paid", "unpaid", "canceled"]`。 |
| `set` + `set_mode: "weighted"` | `values`（字符串 → 权重） | 按权重挑。权重不需要加起来等于 1。例：`"values": {"paid": 0.7, "unpaid": 0.2, "canceled": 0.1}`。 |

### 7.4 日期（`type: "date"`）

| `random_mode` | 必填字段 | 说明 |
|---|---|---|
| `timestamp_range` | `start_time`, `end_time`, `format` | 在 `[start_time, end_time)` 内按秒均匀随机一个时刻（UTC），再按 `format` 格式化成字符串。 |

- `start_time` / `end_time` 是 RFC 3339 格式，如 `"2024-01-01T00:00:00Z"`。
- `format` 是 Go 的时间格式（用"2006-01-02 15:04:05"这个固定时刻来写样式）：
  `"2006-01-02 15:04:05"` → `2024-03-07 18:22:41`，`"2006-01-02"` → `2024-03-07`。

```json
{ "type": "date", "random_mode": "timestamp_range",
  "start_time": "2024-01-01T00:00:00Z", "end_time": "2024-12-31T23:59:59Z",
  "format": "2006-01-02 15:04:05" }
```

（旧版 README 里写的 `"random_mode": "range"`、`"start"`、`"end"` 是错的，代码不认。）

### 7.5 数组（`type: "array"`）——用于 `IN (?)`

| 字段 | 说明 |
|---|---|
| `array_size` | 元素个数，必须 > 0。 |
| `element_type` | 元素类型：`number` / `string` / `date`。 |
| `element_config` | 元素的生成规则，写法和对应类型的参数一样（`type` 可省略）。每个元素独立随机，**可能重复**。 |

```json
{
  "sql": "SELECT * FROM users WHERE id IN (?)",
  "params": [
    { "type": "array", "array_size": 4, "element_type": "number",
      "element_config": { "random_mode": "uniform", "min": 1, "max": 10000 } }
  ]
}
```

实际发出的是 `SELECT * FROM users WHERE id IN (?, ?, ?, ?)`。

### 7.6 `literal`：把值直接拼进 SQL 文本

MySQL 的绑定参数只能出现在"值"的位置，**表名、列名不能是 `?`**。
给参数加上 `"literal": true`，它的值会在发送前直接替换进 SQL 文本，不作为绑定参数：

```json
{ "sql": "SELECT c FROM sbtest? WHERE id = ?",
  "params": [
    { "type": "ref", "ref_name": "tableNum", "literal": true },
    { "type": "number", "random_mode": "uniform", "min": 1, "max": 10000 }
  ] }
```

实际 PREPARE 的是 `SELECT c FROM sbtest37 WHERE id = ?`。每个不同的表名都是一条不同的 prepared statement。

### 7.7 `offset`：在值上加一个固定数

只对整数有效（新生成的或 `ref` 引来的都行）。典型用法是范围查询的上界 = 下界 + 区间长度：

```json
{ "sql": "SELECT c FROM sbtest? WHERE id BETWEEN ? AND ?",
  "params": [
    { "type": "ref", "ref_name": "tableNum", "literal": true },
    { "type": "number", "random_mode": "uniform", "min": 1, "max": 10000, "save_as": "from" },
    { "type": "ref", "ref_name": "from", "offset": 100 }
  ] }
```

### 7.8 参数字段速查

| 字段 | 用于 | 说明 |
|---|---|---|
| `type` | 全部 | `number` / `string` / `date` / `array` / `ref` |
| `random_mode` | number / string / date | 见上 |
| `min`, `max` | number | 整数，含两端 |
| `exponent` | number（幂律） | 浮点数 |
| `partition` | number（分区幂律） | 分段数 |
| `length`, `charset` | string（`random_string`） | 长度；字符集（可选） |
| `format` | string（`number_format`）/ date | `fmt.Sprintf` 格式 / Go 时间格式 |
| `number_config` | string（`number_format`） | 一个数字参数 |
| `set_mode`, `values` | string（`set`） | `uniform` + 数组，或 `weighted` + 对象 |
| `start_time`, `end_time` | date | RFC 3339 |
| `array_size`, `element_type`, `element_config` | array | 见 7.5 |
| `save_as`, `scope` | 除 ref 外 | 存成变量；`transaction`（默认）或 `connection` |
| `ref_name` | ref | 要引用的变量名 |
| `offset` | 整数值 | 加一个固定数 |
| `literal` | 全部 | `true` 时直接拼进 SQL 文本 |

## 8. 参考配置：各种 sysbench JSON

全部对应 `sysbench … --tables=100 --table-size=10000 prepare` 准备的数据（改了表数/行数请同步改 JSON，见 2.2 节）。
默认都是 `concurrency: 40`、`rate_per_thread: 1`（即 40 个事务/秒），压测时请按需调大。
除特别说明外都用基线方式：长连接 + 事务 + 修正版 prepared。

### 8.1 按 sysbench 场景

| 文件 | 对应 sysbench | 每个事务的内容 |
|---|---|---|
| `sysbench-config-oltp-point-select.json` | `oltp_point_select` | 1 条主键点查，不开事务 |
| `sysbench-config-oltp-read-only.json` | `oltp_read_only` | 10 条点查 + 4 种范围查询（简单 / SUM / ORDER BY / DISTINCT） |
| `sysbench-config-oltp-write-only.json` | `oltp_write_only` | 索引列 UPDATE + 非索引列 UPDATE + DELETE 再 INSERT 同一 id |
| `sysbench-config-oltp-read-write-fixprepared.json` | `oltp_read_write` | 上面两者合起来：10 点查 + 4 范围 + 2 UPDATE + DELETE + INSERT |
| `sysbench-config-oltp-update-index.json` | `oltp_update_index` | 1 条 `UPDATE … SET k = k + 1`，不开事务 |
| `sysbench-config-oltp-update-non-index.json` | `oltp_update_non_index` | 1 条 `UPDATE … SET c = ?`，不开事务 |
| `sysbench-config-oltp-insert.json` | `oltp_insert` | 1 条 INSERT（id 自增），不开事务 |

所有 id 都是 `uniform` 均匀分布（和 sysbench 默认的 `--rand-type=special` 不同）。想要热点，把 id 参数换成 `power_law` 或 `partition_power_law` 即可。

### 8.2 同一个 oltp_read_write，不同的执行方式（用来做对比）

| 文件 | 执行方式 | 与基线的区别 |
|---|---|---|
| `sysbench-config-oltp-read-write-fixprepared.json` | **基线**：修正版 prepared | — |
| `sysbench-config-oltp-read-write.json` | 朴素 prepared（每个事务都 PREPARE / CLOSE） | 去掉 `fix_prepared_statement_reuse` |
| `sysbench-config-oltp-read-write-multi.json` | multi-statement（`prepared_cache`） | `multi_statements: true` |
| `sysbench-config-oltp-read-write-pipelined.json` | pipelined binary | `pipelined_binary: true` |

想测短连接（模拟 PHP），把 `oltp-read-write.json` 或 `-multi.json` 的 `connection_type` 改成 `"short"` 即可。
**不要**改 `-fixprepared.json`（见第 5 节），改 `-pipelined.json` 也不会生效。

### 8.3 早期的小配置（冒烟测试用）

| 文件 | 内容 |
|---|---|
| `sysbench-config.json` | 每个连接选 1 张表（1..40），每个事务 10 次 `WHERE id IN (?)` 点查；文本协议（DSN 带 `interpolateParams=true`） |
| `sysbench-config-prepared.json` | 同上，朴素 prepared |
| `sysbench-config-prepared-multi.json` | 同上，multi-statement |
| `config.json` | 几个 `IN (?)` 数组参数的例子（打 `sbtest1..3`），演示 `array` 类型 |

这些配置的表数是 40、id 上限 500000，和 8.1 / 8.2 不同。

### 8.4 完整示例：sysbench-config-oltp-read-write-fixprepared.json

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
    { "sql": "", "comment": "每个连接开局选一张表（1..100），之后一直用它",
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

（`comment` 字段已省略，内容与仓库里的文件一致。）

## 9. OS 调优（短连接高 QPS 时）

`connection_type: "short"` 每个事务都要新建 TCP 连接，客户端源端口和 TIME_WAIT 很快会成为瓶颈。在跑本工具的 Linux 机器上：

```bash
sysctl -w net.ipv4.ip_local_port_range="1024 65535"
sysctl -w net.ipv4.tcp_tw_reuse=1
sysctl -w net.ipv4.tcp_fin_timeout=10
sysctl -w net.netfilter.nf_conntrack_max=1048576
sysctl -w net.netfilter.nf_conntrack_buckets=262144

ulimit -n 1048576
echo "* soft nofile 1048576" >> /etc/security/limits.conf
echo "* hard nofile 1048576" >> /etc/security/limits.conf

# 追加到 /etc/systemd/system.conf 和 /etc/systemd/user.conf
DefaultLimitNOFILE=1048576
# 重启 systemd-logind
systemctl restart systemd-logind
```

工具本身在短连接模式下也会给 socket 设 `SO_REUSEADDR` 和 `SO_LINGER=0`（关闭时直接发 RST，客户端不留 TIME_WAIT），见 `worker/dialer_base.go`。
