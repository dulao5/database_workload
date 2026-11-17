# TiDB Grafana から再現すべき負荷の特徴を取り出す

[English](extract-workload-from-grafana.md) | [中文](extract-workload-from-grafana-zh.md) | 日本語

本ツールで本番の負荷を再現するには、まず **アプリケーションが TiDB をどのように利用しているか** を知る必要があります。
その情報の多くは **TiDB** の Grafana ダッシュボード（`{cluster}-TiDB`、行 **Server**・**Query Summary**・**Query Detail**・**Transaction**・**Executor**）から読み取れます。
このページでは、`database_workload` の各設定について、どのパネルを見れば値を決められるかを対応づけます。

以下のパネル名・メトリクス名は pingcap/tidb master の `pkg/metrics/grafana/tidb.json` に基づいています。古いバージョンではパネルのタイトルが少し異なる場合があります。
時間範囲は必ず **実際のピーク時間帯** を選び、TiDB ノードが複数ある場合は **instance ごと** に見てください（1 ノードだけ全体の合計と違う動きをすることがあります）。

| 知りたいこと | 決まる設定 | 節 |
|---|---|---|
| 長い接続か短い接続か | `connection_type` | [1](#1-長い接続か短い接続か) |
| 読み書きの割合と書き込み量 | `templates` の SQL、`use_transaction` | [2](#2-読み書きの割合と書き込み行数) |
| テキストプロトコルか prepared statement か（再利用の有無） | `use_prepared_statements`、`fix_prepared_statement_reuse` | [3](#3-テキストプロトコルか-prepared-statement-か) |
| multi-statement | `multi_statements`、`multi_statements_mode` | [4](#4-multi-statement) |
| トランザクションとその大きさ | `use_transaction`、テンプレートあたりの文数 | [5](#5-トランザクション) |
| 接続数と処理速度 | `concurrency`、`rate_per_thread` | [6](#6-並列度とレート) |
| Grafana では **分からない** こと | パラメータ、ホットスポット、SQL 文 | [7](#7-grafana-では分からないこと) |

## 1. 長い接続か短い接続か

パネルの場所: **TiDB → Server → Connection Count / Disconnection Count**、および **Query Summary → Command Per Second / CPS By Instance**。

1. **Connection Count**: TiDB ノードごとの接続数は **安定** していますか。
   - 平らな線（例: 常に 200 接続）なら **コネクションプール** を使っています → `"connection_type": "long"`。
   - 大きく上下する、または想定する並列度よりかなり少ない場合は、接続の開閉を繰り返している可能性があります。
2. **Disconnection Count** は *カウンター*（`tidb_server_disconnection_total`）で、パネルでは `rate()` を **かけていません**。
   パネルのクエリを Explore で開き、`sum(rate(tidb_server_disconnection_total[1m]))` のように自分で `rate()` を付けてください。
   その値を、文の処理レート（**Query Summary → QPS → total**）やトランザクションのレート（**Transaction → Transaction OPS**）と比べます。
   - 切断レートが QPS より **ずっと小さい** → 長い接続。
   - 切断レートが **トランザクション（またはリクエスト）のレートに近い** → 1 トランザクションにつきおよそ 1 接続 → `"short"`。
3. **コマンドの種類で裏付ける**: **CPS By Instance**（`tidb_server_query_total`、ラベル `type` / `result`）では、
   短い接続のアプリは接続ごとに `Quit` コマンドを送るため、`Quit` の頻度は切断数とほぼ同じになります。
   `InitDB`（`Use` 文）や `Ping` も頻繁に現れます。
4. **QPS に現れる手がかり**: **Query Summary → QPS**（`tidb_executor_statement_total`、`type` ごと）では、接続まわりの処理が `Use`・`Set`、場合によっては `Rollback` として現れます。
   これらのレートは接続の確立・リセットのレートと関連があります。たとえば、プールが接続を貸し出すときにリセットする場合や、リクエストごとに接続して `SET NAMES ...` / `USE db` を先に実行する場合です。
   これはあくまで手がかりで、証拠ではありません。長い接続のアプリでも別の理由で `SET` を実行することがあります。
5. **接続のリセット**: ドライバやプールによっては *reset connection* や *change user* を送ります。これらのコマンドはパネル上で読める名前になっておらず、
   **CPS By Instance** に **数字** の `type` ラベルとして現れます。`31`（reset connection）と `17`（change user）です。
   レートが高い場合は、プールが利用のたびにセッション状態をリセットしています（接続は長いが、セッション状態は保持されない）。
   本ツールには対応する設定はありません。`"long"` のままにして、この違いを覚えておいてください。

**判断**: 接続数が安定し、切断レートが QPS よりずっと小さい → `long`。切断レートがトランザクション／リクエストのレートに近い → `short`。

## 2. 読み書きの割合と書き込み行数

パネルの場所: **Query Summary → QPS** と **Query Summary → Affected Rows By Type**。

- **QPS** は文の `type`（`Select`・`Insert`・`Update`・`Delete`・`Begin`・`Commit`・`Rollback`・`Set`・`Use` など）ごとのレートを表示します。
  ピーク期間の各タイプの **平均値** を全体に対する割合にします。それが `templates` で再現すべき SQL の割合です
  （例: `Select` 80%、`Update` 15%、`Insert` 5%）。各テンプレートはセッションごとに 1 回実行されるので、`repeat` やテンプレート数で割合を合わせます。
- **Affected Rows By Type**（`tidb_executor_affected_rows`、`sql_type` ごと）は、INSERT/UPDATE/DELETE/REPLACE の **1 秒あたりの書き込み行数** を示します。
  同じタイプの文のレートで割ると *1 文あたりの行数* が分かり、`UPDATE` が 1 行の更新か範囲更新か、`INSERT` が 1 行か複数行（`VALUES (...),(...)`、配列パラメータ）かを判断できます。
- QPS の `Begin`/`Commit`/`Rollback` は「業務の処理」ではなく、明示的なトランザクションを使っているかどうかの手がかりです（第 5 節）。読み書きの割合の計算からは除外してください。

## 3. テキストプロトコルか prepared statement か

パネルの場所: **Query Summary → CPS By Instance**、**Server → Prepare Statement Count**、**Executor → Queries Using Plan Cache OPS**。

**CPS By Instance**（`tidb_server_query_total`）が数えるのは SQL 文ではなく MySQL の *コマンド* です。`type` と `result` のラベルを見ます。

| `type`（result が OK） | プロトコル上の意味 |
|---|---|
| `Query` | テキストプロトコル（`COM_QUERY`）。生の SQL、SQL として送る `PREPARE`/`EXECUTE`、multi-statement を含む |
| `StmtPrepare` | `COM_STMT_PREPARE`（バイナリプロトコル） |
| `StmtExecute` | `COM_STMT_EXECUTE` |
| `StmtClose` | `COM_STMT_CLOSE` |

比率の読み方:

- ほぼ `Query` だけ → テキストプロトコル → `"use_prepared_statements": false`。
- `StmtExecute` が大半で、`StmtPrepare` と `StmtClose` がごくわずか → prepared statement が **再利用** されている（接続ごとに 1 回だけ prepare）→ `"use_prepared_statements": true` と `"fix_prepared_statement_reuse": true`。
- `StmtPrepare` ≈ `StmtExecute`（約 1:1 で、`StmtClose` も近い値）→ 実行のたびに prepare して閉じている（多くのドライバの既定である「素朴な」使い方）→ `"use_prepared_statements": true`、`fix_prepared_statement_reuse` は **付けない**。
- 両方が混在 → 一部の文だけ prepare を使っています。割合の大きいほうに合わせるか、負荷を 2 つに分けて実行してください。
- **Server → Prepare Statement Count**（`tidb_server_prepared_stmts`）も確認します。小さく安定した値は正常で、増え続ける場合はクライアントが prepare するだけで close していません。
- **Executor → Queries Using Plan Cache OPS** は plan cache にヒットした文の数を示します（既定では prepared statement のみ。non-prepared plan cache を有効にした場合を除く）。
- 失敗したコマンド（`result=Error`）も同じ比率に含まれます。エラーが多い場合は、先にエラーの原因を調べてください。本来の業務負荷はパネルの見た目と違うかもしれません。

## 4. multi-statement

パネルの場所: **TiDB → Query Detail → Queries In Multi-Statement**。

このパネルは **1 つの multi-query リクエストに含まれるクエリ数** を表示します。`avg`（リクエストあたりのクエリ数）と `sum`（1 秒あたりのクエリ数。`tidb_server_multi_query_num` から算出）です。

- `sum` が 0、またはパネルが空 → アプリは multi-statement を使っていません。
- `sum` > 0 → アプリは 1 回の `COM_QUERY` に `;` 区切りで複数の文を送っています。`avg` がバッチの大きさで、トランザクションのテンプレートの文数とおおよそ一致するはずです。
  `"multi_statements": true` を使います（`multi_statements_mode` は README を参照）。
- multi-statement のリクエストは **CPS By Instance** では `Query` として数えられます。そのため `Query` の割合が高く、かつ multi-statement のパネルが 0 でない場合、**1 文ごとに 1 コマンドということではありません**。

## 5. トランザクション

パネルの場所: **TiDB → Transaction → Transaction OPS / Transaction Statement Num / Duration**、および **QPS** の `Begin`/`Commit`/`Rollback`。

- **Transaction OPS** はトランザクションのレートです。QPS に `Begin`/`Commit` があり（Transaction OPS とも対応する）、アプリは明示的なトランザクションを使っています → `"use_transaction": true`。
  Transaction OPS が DML のレートとほぼ同じで `Begin` がない場合は、autocommit で 1 文 = 1 トランザクションです → `"use_transaction": false`。
- **Transaction Statement Num**（ヒストグラム）は、1 トランザクションに含まれる文の数を示します。セッションあたりの文数（テンプレート数 × `repeat`）を決める材料にします。
- **Duration**（P99/P95）はトランザクションの継続時間です。各文のレイテンシの合計よりずっと長い場合、アプリはトランザクションを開いたまま別の処理をしています。本ツールには think time の設定がないため、この差を覚えておいてください。
- QPS の `Rollback`: ロールバックが多い場合は、競合やリトライ、またはプールが返却時にロールバックしている可能性があります（第 1 節）。

## 6. 並列度とレート

パネルの場所: **Server → Connection Count**、**Transaction OPS**、**Query Summary → Connection Idle Duration**。

- `concurrency` = ワーカー数 = 接続数です。長い接続のアプリでは、ピーク時の **Connection Count** の **total** の線を使います。
  **active connections** の線（`tidb_server_tokens`）は実際に実行中の接続数です。プールが実際の並列度よりずっと大きい場合は、こちらのほうが適切です。
  短い接続では *接続数は並列度ではありません*（接続が現れては消えるため）。**Transaction OPS** と **Duration** から、`リクエストレート × 平均レイテンシ`（リトルの法則）で並列度を見積もります。
- `rate_per_thread` =（TiDB ノードあたりのトランザクションレート）÷（そのノードで模擬する `concurrency`）で、ワーカー 1 つあたりの 1 秒あたりセッション数です。
  `0` は「制限なし」で、最大スループットを測るときに使います。再現（リプレイ）には向きません。
- **Connection Idle Duration**（`tidb_server_conn_idle_duration_seconds`、`in_txn` が 1/0）は、コマンドとコマンドの間に接続がアイドルしている時間です。
  `in_txn=1` はトランザクション内でのアプリの「think time」、`in_txn=0` はトランザクションの間です。
  アイドル時間が長い場合、各ワーカーはほとんどの時間アプリを待っています。現実的な負荷にするには、`rate_per_thread` を下げて `concurrency` を増やします。

本番の TiDB ノードが複数ある場合は、1 台の TiDB に対して 1 ノード分を再現するか、数値をノード数で割ってロードバランサーに接続させてください。

## 7. Grafana では分からないこと

負荷の次の部分は Grafana のダッシュボードにありません。`params` を埋める前に、ほかの場所から入手してください。

| 足りない情報 | 入手先 |
|---|---|
| SQL 文／テンプレートとその割合 | `information_schema.statements_summary`（`cluster_statements_summary`）、Top SQL、スロークエリログ |
| パラメータの分布（ホットな id、範囲、`IN (...)` の大きさ） | スロークエリログ／statement summary のサンプル。ホットスポットは **TiDB Dashboard → Key Visualizer** と **PD → Hot Region** パネル |
| 結果のサイズ | **Server → Client Data Traffic**、`Affected Rows`、スローログの `Result_rows` |
| データ量とテーブル構成 | `information_schema.tables`、TiDB Dashboard → クラスター情報 → 統計情報 |
| 分離レベル、悲観／楽観、autocommit | `SHOW VARIABLES`、接続文字列のセッション変数 |
| アプリと TiDB の間のネットワーク遅延 | アプリのホストから計測します。本ツールは起動したマシン上で動きます |

Grafana が示すのは **時間窓の平均値** です（`rate()` の窓は 1 分）。突発的な負荷や日々のピークは見逃されることがあります。数値を決める前に、複数のピーク時間帯を比較してください。
