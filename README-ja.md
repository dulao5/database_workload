# database_workload

[English](README.md) | [中文](README-zh.md) | 日本語

**JSON の設定どおりに MySQL / TiDB へ SQL を送り続ける負荷生成ツールです。**
SQL のテンプレートと「パラメータをどうランダムにするか」を書けば、N 並列・指定のレートで、Ctrl-C を押すまで走り続けます。

再現できること：

- **接続のしかた**：長い接続（コネクションプールを持つアプリ）か、トランザクションごとに新しい接続（プールを持たないアプリ）か。
- **SQL の送り方**：普通のテキストクエリ、サーバー側プリペアドステートメント、トランザクション全体を 1 往復で送る方式（multi-statement / pipelined binary）。
- **パラメータの分布**：一様、べき乗則（ホットスポット）、分割べき乗則。さらに「1 つのトランザクション内の複数の SQL で同じランダム id を使う」「接続ごとに 1 つのテーブルだけを使う」といった関連づけ。

**しない**こと：

- **データは作りません。** テーブルとデータは先に `sysbench prepare`（または別のツール）で用意してください。[2 章](#2-クイックスタート)を参照。
- **QPS やレイテンシは出力しません。** ログには開始・停止・エラーだけが出ます。スループットとレイテンシはデータベース側（TiDB Dashboard、Grafana、`information_schema.statements_summary` など）で見てください。

---

## 目次

1. [動作モデル：実際に何をしているか](#1-動作モデル実際に何をしているか)
2. [クイックスタート](#2-クイックスタート)
3. [トップレベルの設定項目](#3-トップレベルの設定項目)
4. [実行方式の一覧（1 トランザクションでネットワークに何が流れるか）](#4-実行方式の一覧1-トランザクションでネットワークに何が流れるか)
5. [一緒に使えない組み合わせ](#5-一緒に使えない組み合わせ)
6. [SQL テンプレート（templates）](#6-sql-テンプレートtemplates)
7. [パラメータ（params）の生成のしかた](#7-パラメータparamsの生成のしかた)
8. [参考設定：sysbench 相当の JSON](#8-参考設定sysbench-相当の-json)
9. [OS チューニング（短い接続で高 QPS のとき）](#9-os-チューニング短い接続で高-qps-のとき)

---

## 1. 動作モデル：実際に何をしているか

```
concurrency 個の worker（goroutine）を起動する。各 worker が持つ DB 接続はちょうど 1 本
各 worker はくり返す：
    "session" を 1 回実行する：
        （use_transaction のとき）BEGIN
        templates[0], templates[1], ... を順に実行（各テンプレートを repeat 回）
            実行のたびに params からパラメータを作り直す
        （use_transaction のとき）COMMIT。エラーなら ROLLBACK
    rate_per_thread の次の拍まで待つ
Ctrl-C（SIGINT）か SIGTERM を受けるまで
```

- **1 session ＝ すべてのテンプレートを最初から最後まで 1 回通すこと。** `use_transaction` を有効にすれば 1 トランザクションです。
- 全体のスループットの上限 ≈ `concurrency × rate_per_thread` session/秒。
- session の中で 1 つでも SQL が失敗すると、その session は中断（ロールバック）され、`ERROR` のログが 1 行出て、次の session に進みます。

## 2. クイックスタート

### 2.1 ビルド

```bash
go build -o database_workload .
```

### 2.2 sysbench でデータを作り、このツールで負荷をかける（おすすめ）

このツールは SQL を流すだけで、データは作りません。いちばん手軽なのは、sysbench の `prepare` でテーブルを作ってデータを入れ、そのあとこのツールで好きな形の負荷をかける方法です。
sysbench が作るテーブルは `sbtest1..sbtestN`、構造は `id INT AUTO_INCREMENT PRIMARY KEY, k INT, c CHAR(120), pad CHAR(60), KEY k_1(k)` で、8 章の参考設定とそのまま合います。

```bash
# 1) 100 テーブル × 各 10000 行を作る（データベース test は先に作っておく）
sysbench oltp_read_write \
  --mysql-host=127.0.0.1 --mysql-port=4000 --mysql-user=root --mysql-db=test \
  --tables=100 --table-size=10000 --threads=16 \
  prepare

# 2) このツールで負荷をかける
./database_workload -config sysbench-config-oltp-read-write-fixprepared.json

# 3) Ctrl-C で止める
```

**大事な点：JSON の数値の範囲を `prepare` の指定に合わせてください。**

| sysbench の指定 | JSON の場所 | 参考設定の値 |
|---|---|---|
| `--tables=100` | テーブルを選ぶパラメータ（`"save_as": "tableNum"`）の `max` | `100` |
| `--table-size=10000` | すべての id パラメータの `max` | `10000` |
| `--mysql-db=test` | `db_conn_str` のデータベース名 | `/test` |

範囲が実データより大きくてもエラーにはなりません（SELECT が空になり、UPDATE が 0 行になるだけ）。ただし負荷の中身は想定と違うものになります。

なぜ `sysbench run` をそのまま使わないのか：sysbench は長い接続しか使えず、プリペアドステートメントの使い方も sysbench のやり方に固定です。
このツールなら、同じデータに対して短い接続・テキストプロトコル・multi-statement・pipelined binary・別のホットスポット分布に切り替えて比べられます。

### 2.3 コマンドライン引数

1 つだけです：`-config <path>`、既定値は `config.json`。

---

## 3. トップレベルの設定項目

| 項目 | 型 | 既定値 | 意味 |
|---|---|---|---|
| `db_conn_str` | string | （必須） | [go-sql-driver/mysql の DSN](https://github.com/go-sql-driver/mysql#dsn-data-source-name)。例：`root:@tcp(127.0.0.1:4000)/test?parseTime=true&timeout=3s&readTimeout=3s&writeTimeout=3s`。ネットワーク障害で worker が止まらないよう、タイムアウトを付けることをおすすめします。 |
| `concurrency` | int | `0` | worker の数。**1 つの worker がちょうど 1 本の接続を使う**ので、DB への接続数でもあります。 |
| `rate_per_thread` | int | `0` | worker 1 つあたり毎秒何 session 実行するか（トランザクションありならトランザクション数）。`0` または省略で無制限（終わったらすぐ次へ）。1 session が `1/rate` 秒より長くかかると、実際のレートは設定より下がります（取りこぼした分を後で埋めることはしません）。 |
| `connection_type` | string | `"long"` | `"long"`：長い接続。`"short"`：session ごとに接続を開いて閉じる。下で詳しく説明します。 |
| `use_transaction` | bool | `false` | session 内の SQL をすべて `BEGIN … COMMIT` で囲むか（TiDB の既定は悲観的トランザクション）。 |
| `use_prepared_statements` | bool | `false` | `true`：サーバー側プリペアドステートメント（バイナリプロトコル、`COM_STMT_PREPARE` / `COM_STMT_EXECUTE`）。`false`：テキストプロトコル（`COM_QUERY`）。 |
| `fix_prepared_statement_reuse` | bool | `false` | 「トランザクションのたびに PREPARE し直して CLOSE する」問題を直し、1 つの文を接続ごとに 1 回だけ PREPARE して、あとは EXECUTE をくり返すようにします。下で詳しく説明します。 |
| `multi_statements` | bool | `false` | トランザクション全体を**1 つの**テキストプロトコルの要求にまとめて送ります（1 往復）。下で詳しく説明します。 |
| `multi_statements_mode` | string | `"prepared_cache"` | `multi_statements: true` のときだけ意味があります。`"prepared_cache"` か `"raw"`。 |
| `pipelined_binary` | bool | `false` | トランザクション内の EXECUTE パケットをバイナリプロトコルで続けて書き出し、まとめて応答を読みます（1 往復）。下で詳しく説明します。 |
| `templates` | array | （必須） | SQL テンプレートの一覧。[6 章](#6-sql-テンプレートtemplates)を参照。 |

JSON の知らないキーは無視されるので、`"comment"` を自由に書いてコメントにできます（参考設定でもそうしています）。

### `connection_type`：長い接続か、短い接続か

- **`"long"`（既定）**：worker ごとに接続を 1 本開き、使い続けます。エラーが起きたときだけ接続し直します。
  再現できるもの：
  - **コネクションプールを持つアプリ**：Go（`database/sql`）、Java（HikariCP、Druid など）、.NET（ADO.NET の接続プール）、Node.js（`mysql2` の pool）、
    Python（SQLAlchemy の接続プール）、Ruby on Rails（ActiveRecord の接続プール）、Rust（`sqlx` の pool）。
  - **sysbench のようなベンチマークツール**：最初に接続を取り、最後まで使い続けるもの。
  - 接続単位の状態（プリペアドステートメント、`scope: "connection"` の変数）はトランザクションをまたいで残ります。
- **`"short"`**：**session のたびに新しい接続を開き、終わったら閉じます。**
  再現できるもの：
  - **コネクションプールを持たないアプリ**：PHP（PHP-FPM ＋ mysqli / PDO、永続接続なし）が代表例です。
  - Python の Django（既定の `CONN_MAX_AGE=0` では、リクエストが終わるたびに切断します）。
  - Perl / Python の CGI スクリプト、cron のバッチ、シェルから `mysql` コマンドを呼ぶ処理。
  - 接続を使い回さない Serverless 関数（呼び出しのたびに接続する AWS Lambda など）。
  - この種の負荷では、トランザクションごとに TCP ハンドシェイクと MySQL の認証がかかり、接続の確立と破棄そのものが負荷になります。
  - 短い接続は送信元ポートを早く使い切ります。高 QPS のときは [9 章](#9-os-チューニング短い接続で高-qps-のとき)を見てください。

### `use_prepared_statements` と `fix_prepared_statement_reuse`

- `use_prepared_statements: false`：すべての SQL をテキストで送ります（`COM_QUERY`）。
  - 注意：DSN に `interpolateParams=true` が**ない**と、go-sql-driver は `?` の引数を持つテキストクエリを、内部で黙って PREPARE → EXECUTE → CLOSE にします。
    本当に「1 文 1 テキスト要求」にしたいときは、DSN に `interpolateParams=true` を付けてください（クライアント側で SQL を組み立ててから送ります）。
- `use_prepared_statements: true`：サーバー側プリペアドステートメント。ただし落とし穴があります：
  - `use_transaction` と一緒に使うと、Go 標準ライブラリ `database/sql` の `Tx.StmtContext` が**トランザクションのたびに PREPARE し直し、コミット時に CLOSE します**。
    つまりキャッシュが効かず、毎トランザクション PREPARE → EXECUTE → CLOSE になります。これは「プリペアドステートメントを使い回しているつもり」の Go アプリをそのまま再現しているとも言えます。
  - トランザクションなしなら、この問題は起きず、文は普通に使い回されます。
- `fix_prepared_statement_reuse: true`：上の落とし穴を避けます。BEGIN/COMMIT をテキストで送り、`*sql.Tx` を通さずにキャッシュした文を直接実行します。
  結果：**1 つの文は接続ごとに 1 回だけ PREPARE され、あとは EXECUTE だけ**になります。sysbench や、Java で `cachePrepStmts=true` にしたときと同じ動きです。
  比較するときは、ふつうこれを基準にします。

### `multi_statements` と `multi_statements_mode`

トランザクション全体（BEGIN、すべての文、COMMIT）を `;` でつないで**1 つの**テキストプロトコルの要求（1 つの `COM_QUERY`）にして送ります。1 往復です。
[`github.com/dulao5/tidb-multistmt`](https://github.com/dulao5/tidb-multistmt) で実装しています。有効にすると、DSN の末尾に `multiStatements=true` が自動で付きます。

- `"prepared_cache"`（既定）：テキストプロトコルの中で SQL レベルの prepare を使います。
  ある文がその接続で初めて出てきたときに `PREPARE` を送り（以後は物理接続ごとにキャッシュするので、短い接続でも正しく動きます）、
  実行のたびに `SET @p=…; EXECUTE stmt USING @p…` を送ります。つまり「テキストプロトコルで SET も送るが、prepare の最適化はある」方式です。
- `"raw"`：prepare を使いません。引数の値をリテラルとして SQL 文に埋め込み、トランザクション全体を `;` でつないだ 1 つの文字列として送ります。
  「multi-statement だがプリペアドステートメントを一切使わない」場合の対照群です。

### `pipelined_binary`

**multi-statement のバイナリプロトコル版**です。[`github.com/dulao5/tidb-binary-multistmt`](https://github.com/dulao5/tidb-binary-multistmt) で実装しています。
BEGIN を送ったあと、トランザクション内の `COM_STMT_EXECUTE` パケットを一気に続けて書き出し、すべての応答を順に読み、その結果で COMMIT か ROLLBACK を決めます。
1 往復で、`SET @…` のテキストのオーバーヘッドがなく、文の PREPARE も接続ごとに 1 回だけです。

- 悲観的トランザクションだけに対応します。途中の文が失敗しても、すでに書き出した後続の文は実行されます。最後の ROLLBACK で全体を取り消します。
- 有効にすると、**`use_transaction` / `use_prepared_statements` / `multi_statements` / `fix_prepared_statement_reuse` / `connection_type` は効かなくなります**。
  常に「トランザクション ＋ バイナリのプリペアド ＋ 長い接続」です。

## 4. 実行方式の一覧（1 トランザクションでネットワークに何が流れるか）

1 トランザクションに N 個の文があるとします：

| 方式 | 設定 | 1 トランザクションの往復数 | 1 トランザクションの PREPARE |
|---|---|---|---|
| テキスト、トランザクションなし | `use_transaction:false`、`use_prepared_statements:false`、DSN に `interpolateParams=true` | N | 0 |
| テキスト ＋ トランザクション | `use_transaction:true`、`use_prepared_statements:false`、DSN に `interpolateParams=true` | N + 2 | 0 |
| プリペアド（素朴版） | `use_transaction:true`、`use_prepared_statements:true` | 2N + 2（文ごとに PREPARE ＋ EXECUTE、コミット時に CLOSE） | N |
| **プリペアド（修正版、基準）** | 上に `fix_prepared_statement_reuse:true` を足す | N + 2 | 初回だけ |
| multi-statement | `use_transaction:true`、`use_prepared_statements:true`、`multi_statements:true` | 1 | 初回だけ（テキストの `PREPARE`） |
| multi-statement raw | 上に `multi_statements_mode:"raw"` を足す | 1 | 0 |
| pipelined binary | `pipelined_binary:true` | 1（BEGIN は別に 1 回） | 初回だけ |

「初回だけ」は長い接続の場合です。短い接続では session ごとに接続が新しくなるので、毎回 PREPARE し直します。

## 5. 一緒に使えない組み合わせ

以下はすべて TiDB v8.5 で実際に動かして確かめました。

**起動時にエラーで止まる：**

| 組み合わせ | エラー |
|---|---|
| `multi_statements:true` なのに `use_transaction` か `use_prepared_statements` が `true` でない | `multi_statements requires use_transaction and use_prepared_statements to both be true` |
| `fix_prepared_statement_reuse:true` なのに `use_transaction` / `use_prepared_statements` が `true` でない、または `multi_statements` も有効 | `fix_prepared_statement_reuse requires …` |
| `multi_statements_mode` が `"prepared_cache"` / `"raw"` / 空 のどれでもない | `multi_statements_mode must be …` |
| `pipelined_binary:true` と `multi_statements:true` で、`use_transaction` がない | 1 行目と同じ（`multi_statements` の検査が先に行われる） |

**起動はするが、実行中にエラーになる：**

| 組み合わせ | 起きること |
|---|---|
| `connection_type:"short"` ＋ `fix_prepared_statement_reuse:true` | 最初のトランザクションは成功しますが、**以後はすべて失敗します**：`sql: connection is already closed`（キャッシュした文が、閉じた古い接続に結びついたままのため）。短い接続では `fix_prepared_statement_reuse` を使わないでください。 |
| テーブル名などの識別子の位置に `?` を使い、そのパラメータに `"literal": true` がなく、サーバー側 prepare の経路（`use_prepared_statements`、`multi_statements`、`pipelined_binary`、または DSN に `interpolateParams=true` がないテキストモード）を通る | `Error 1064 … syntax error`。MySQL はテーブル名をバインドできないためです。[7.6](#76-literal値をそのまま-sql-文に埋め込む) を参照。 |
| `type:"ref"` が、まだ作られていない変数を参照している | `session variable "xxx" referenced before it was saved`。すべての session が失敗します。 |

**動くが、一部の設定が黙って無視される：**

| 組み合わせ | 無視されるもの |
|---|---|
| `pipelined_binary:true` | `use_transaction`、`use_prepared_statements`、`multi_statements`、`multi_statements_mode`、`fix_prepared_statement_reuse`、`connection_type`（常に長い接続） |
| `multi_statements:false` で `multi_statements_mode` を指定 | `multi_statements_mode` |

**安心して組み合わせられるもの**：短い接続 ＋ テキスト、短い接続 ＋ 素朴版プリペアド、短い接続 ＋ multi-statement（どちらのモードでも）。

## 6. SQL テンプレート（templates）

```jsonc
{
  "sql": "SELECT c FROM sbtest? WHERE id = ?",
  "repeat": 10,
  "comment": "自由に書けます。無視されます",
  "params": [ { …1 つ目の ? のパラメータ… }, { …2 つ目の ? のパラメータ… } ]
}
```

| 項目 | 意味 |
|---|---|
| `sql` | SQL 文。`?` がプレースホルダーです。**`?` の数と `params` の数は同じにしてください**。順番に対応します。 |
| `params` | それぞれの `?` の値の作り方。7 章を参照。 |
| `repeat` | このテンプレートを 1 session の中で続けて何回実行するか。既定は 1。毎回パラメータを作り直します。 |
| `sql` が空文字列 | 「作るだけで実行しない」テンプレート。パラメータはふつうに作られますが（たいていは `save_as` で変数に保存するため）、SQL は送りません。参考設定では、接続の最初にテーブルを選ぶのに使っています。 |

**`IN (?)` は自動で展開されます**：パラメータが配列（`type: "array"`）を作る場合、`IN (?)` は要素の数だけ `IN (?, ?, ?, ?)` に展開されます。

## 7. パラメータ（params）の生成のしかた

### 7.1 寿命：いつ新しい値が作られるか

- **既定**：どのパラメータも、**実行のたびに新しくランダムに作られます**。`repeat: 10` なら 10 回それぞれ独立した値です。
- **`save_as` ＋ `scope: "transaction"`**（`scope` を省略したときの既定）：作った値を変数として保存し、**同じ session（トランザクション）の中で**あとのパラメータが `ref` で同じ値を使えます。新しい session が始まるたびに消えます。
  例：ランダムな id を DELETE し、同じ id で INSERT し直す。
- **`save_as` ＋ `scope: "connection"`**：**worker ごとに 1 回だけ**作り、以後ずっとその値を使います。
  例：sysbench と同じように、接続の最初にテーブルを 1 つ選び、以後のトランザクションはすべてそのテーブルだけを使う。
  注意：結びつく先は worker で、物理接続ではありません。短い接続で接続が変わっても値は変わりません。
- **`type: "ref"`**：何も作らず、参照するだけです。まず session の変数を、次に connection の変数を探します。参照する変数は**それより前**（前のテンプレート、または同じテンプレートの前のパラメータ）で作られている必要があります。

### 7.2 数値（`type: "number"`）

| `random_mode` | 必須の項目 | 分布 |
|---|---|---|
| `uniform` | `min`, `max` | `[min, max]` の一様分布（両端を含む）。 |
| `power_law` | `min`, `max`, `exponent` | べき乗則：**`min` に近いほどよく出ます**。`exponent` が大きいほど集中します。`exponent` に `1.0` は使えません。 |
| `partition_power_law` | `min`, `max`, `exponent`, `partition` | `[min, max]` を `partition` 個の区間に等分し、一様に 1 区間を選んで、その中でべき乗則で選びます。結果として**ばらけたホットスポットがたくさん**できます（各区間の先頭に 1 つ）。 |

**実際の分布の形**（それぞれ 20 万回サンプリングした実測）：

| 設定 | 中央値 | 90 パーセンタイル | いちばんよく出る値 |
|---|---|---|---|
| `uniform` 1..10000 | 5001 | 8979 | どの値も約 0.01% |
| `power_law` 1..1000000、`exponent: 2` | 2 | 10 | 値 1 が 33%、1〜5 で合わせて約 82% |
| `power_law` 1..1000000、`exponent: 1.01` | 781 | 229088 | 1〜5 で合わせて約 13%、長い裾が全範囲に広がる |
| `power_law` 1..1000000、`exponent: 0.5` | 251153 | 809958 | はっきりしたホットスポットはなく、小さい値に寄る |
| `partition_power_law` 1..1000000、`exponent: 2`、`partition: 10` | — | — | ホットスポット 10 個（各区間の最初の値）、それぞれ約 3.4% |

使うときの注意：

- `power_law` の形は `max` で決まり、`min` は全体をずらすだけです。コードは `[1, max]` でべき乗則の値を取り、`min - 1` を足し、`max` を超えた分は `max` に切り詰めます。
  そのため `min` が 1 でないと、一部の値が `max` に溜まります。
- `partition_power_law` で `exponent` が 1 に近い（1.001 など）と、区間内のべき乗則の値が区間の長さを超えることが多く、**区間の最後の値**に切り詰められます。
  ホットスポットが区間の先頭ではなく末尾に移ります。「各区間の先頭にはっきりしたホットスポット」がほしいときは、`exponent` を 1.5 以上にしてください。
- `partition_power_law` で `exponent: 1.0` としてもエラーにはならず、区間内は一様分布になります。

### 7.3 文字列（`type: "string"`）

| `random_mode` | 必須の項目 | 説明 |
|---|---|---|
| `random_string` | `length` | `length` 文字のランダムな文字列。`charset`（文字の集合）は省略可、既定は `a-zA-Z0-9`。例：sysbench の `c CHAR(120)` なら `length: 120`。 |
| `number_format` | `format`, `number_config` | まず `number_config`（数値のパラメータ。`type` は省略可）で整数を作り、Go の `fmt.Sprintf(format, n)` で整形します。例：`"format": "user_%d"` → `user_42`、`"%08d"` → `00000042`。数値側のホットスポット分布がそのまま引き継がれます。 |
| `set` ＋ `set_mode: "uniform"` | `values`（文字列の配列） | 配列から同じ確率で 1 つ選びます。例：`"values": ["paid", "unpaid", "canceled"]`。 |
| `set` ＋ `set_mode: "weighted"` | `values`（文字列 → 重み） | 重みに従って選びます。重みの合計は 1 でなくてかまいません。例：`"values": {"paid": 0.7, "unpaid": 0.2, "canceled": 0.1}`。 |

### 7.4 日付（`type: "date"`）

| `random_mode` | 必須の項目 | 説明 |
|---|---|---|
| `timestamp_range` | `start_time`, `end_time`, `format` | `[start_time, end_time)` の中から秒単位で一様に時刻を選び（UTC）、`format` で文字列にします。 |

- `start_time` / `end_time` は RFC 3339 形式です。例：`"2024-01-01T00:00:00Z"`。
- `format` は Go の時刻レイアウト（基準時刻「2006-01-02 15:04:05」で形を書きます）：
  `"2006-01-02 15:04:05"` → `2024-03-07 18:22:41`、`"2006-01-02"` → `2024-03-07`。

```json
{ "type": "date", "random_mode": "timestamp_range",
  "start_time": "2024-01-01T00:00:00Z", "end_time": "2024-12-31T23:59:59Z",
  "format": "2006-01-02 15:04:05" }
```

（以前の README にあった `"random_mode": "range"`、`"start"`、`"end"` は誤りで、コードは受け付けません。）

### 7.5 配列（`type: "array"`）— `IN (?)` 用

| 項目 | 説明 |
|---|---|
| `array_size` | 要素の数。0 より大きくしてください。 |
| `element_type` | 要素の型：`number` / `string` / `date`。 |
| `element_config` | 要素の作り方。その型のパラメータと同じ書き方です（`type` は省略可）。要素はそれぞれ独立に作られるので、**重複することがあります**。 |

```json
{
  "sql": "SELECT * FROM users WHERE id IN (?)",
  "params": [
    { "type": "array", "array_size": 4, "element_type": "number",
      "element_config": { "random_mode": "uniform", "min": 1, "max": 10000 } }
  ]
}
```

実際に送られるのは `SELECT * FROM users WHERE id IN (?, ?, ?, ?)` です。

### 7.6 `literal`：値をそのまま SQL 文に埋め込む

MySQL のバインドパラメータは「値」の位置にしか置けません。**テーブル名や列名を `?` にはできません。**
パラメータに `"literal": true` を付けると、その値はバインドされず、送る前に SQL 文に直接埋め込まれます：

```json
{ "sql": "SELECT c FROM sbtest? WHERE id = ?",
  "params": [
    { "type": "ref", "ref_name": "tableNum", "literal": true },
    { "type": "number", "random_mode": "uniform", "min": 1, "max": 10000 }
  ] }
```

実際に PREPARE されるのは `SELECT c FROM sbtest37 WHERE id = ?` です。テーブル名が違えば、別のプリペアドステートメントになります。

### 7.7 `offset`：値に決まった数を足す

整数にだけ使えます（新しく作った値でも、`ref` で取ってきた値でも可）。よくある使い方は、範囲検索の上限 ＝ 下限 ＋ 範囲の幅 です：

```json
{ "sql": "SELECT c FROM sbtest? WHERE id BETWEEN ? AND ?",
  "params": [
    { "type": "ref", "ref_name": "tableNum", "literal": true },
    { "type": "number", "random_mode": "uniform", "min": 1, "max": 10000, "save_as": "from" },
    { "type": "ref", "ref_name": "from", "offset": 100 }
  ] }
```

### 7.8 パラメータの項目一覧

| 項目 | 使う型 | 説明 |
|---|---|---|
| `type` | すべて | `number` / `string` / `date` / `array` / `ref` |
| `random_mode` | number / string / date | 上を参照 |
| `min`, `max` | number | 整数、両端を含む |
| `exponent` | number（べき乗則） | 浮動小数点数 |
| `partition` | number（分割べき乗則） | 区間の数 |
| `length`, `charset` | string（`random_string`） | 長さ、文字の集合（省略可） |
| `format` | string（`number_format`）/ date | `fmt.Sprintf` の書式 / Go の時刻レイアウト |
| `number_config` | string（`number_format`） | 数値のパラメータ |
| `set_mode`, `values` | string（`set`） | `uniform` ＋ 配列、または `weighted` ＋ オブジェクト |
| `start_time`, `end_time` | date | RFC 3339 |
| `array_size`, `element_type`, `element_config` | array | 7.5 を参照 |
| `save_as`, `scope` | ref 以外 | 変数として保存。`transaction`（既定）か `connection` |
| `ref_name` | ref | 参照する変数の名前 |
| `offset` | 整数の値 | 決まった数を足す |
| `literal` | すべて | `true` なら SQL 文に直接埋め込む |

## 8. 参考設定：sysbench 相当の JSON

どれも `sysbench … --tables=100 --table-size=10000 prepare` で用意したデータを前提にしています（テーブル数や行数を変えたら JSON も合わせてください。2.2 を参照）。
既定は `concurrency: 40`、`rate_per_thread: 1`（つまり毎秒 40 トランザクション）なので、負荷をかけるときは必要に応じて上げてください。
特に断りがなければ、基準の方式（長い接続 ＋ トランザクション ＋ 修正版プリペアド）です。

### 8.1 sysbench のシナリオ別

| ファイル | 対応する sysbench | 1 トランザクションの中身 |
|---|---|---|
| `sysbench-config-oltp-point-select.json` | `oltp_point_select` | 主キーでの検索 1 回、トランザクションなし |
| `sysbench-config-oltp-read-only.json` | `oltp_read_only` | 主キー検索 10 回 ＋ 範囲検索 4 種（単純 / SUM / ORDER BY / DISTINCT） |
| `sysbench-config-oltp-write-only.json` | `oltp_write_only` | インデックス列の UPDATE ＋ インデックスなし列の UPDATE ＋ DELETE して同じ id で INSERT |
| `sysbench-config-oltp-read-write-fixprepared.json` | `oltp_read_write` | 上の 2 つを合わせたもの：主キー検索 10 ＋ 範囲 4 ＋ UPDATE 2 ＋ DELETE ＋ INSERT |
| `sysbench-config-oltp-update-index.json` | `oltp_update_index` | `UPDATE … SET k = k + 1` を 1 回、トランザクションなし |
| `sysbench-config-oltp-update-non-index.json` | `oltp_update_non_index` | `UPDATE … SET c = ?` を 1 回、トランザクションなし |
| `sysbench-config-oltp-insert.json` | `oltp_insert` | INSERT を 1 回（id は自動採番）、トランザクションなし |

id はすべて `uniform`（一様分布）です（sysbench の既定 `--rand-type=special` とは違います）。ホットスポットがほしいときは、id のパラメータを `power_law` か `partition_power_law` に変えてください。

### 8.2 同じ oltp_read_write を、実行方式だけ変えたもの（比較用）

| ファイル | 実行方式 | 基準との違い |
|---|---|---|
| `sysbench-config-oltp-read-write-fixprepared.json` | **基準**：修正版プリペアド | — |
| `sysbench-config-oltp-read-write.json` | 素朴版プリペアド（トランザクションのたびに PREPARE / CLOSE） | `fix_prepared_statement_reuse` なし |
| `sysbench-config-oltp-read-write-multi.json` | multi-statement（`prepared_cache`） | `multi_statements: true` |
| `sysbench-config-oltp-read-write-pipelined.json` | pipelined binary | `pipelined_binary: true` |

短い接続（PHP の再現）を試すときは、`oltp-read-write.json` か `-multi.json` の `connection_type` を `"short"` にしてください。
`-fixprepared.json` では**しないでください**（5 章を参照）。`-pipelined.json` では変えても効きません。

### 8.3 初期の小さな設定（動作確認用）

| ファイル | 内容 |
|---|---|
| `sysbench-config.json` | 接続ごとにテーブルを 1 つ選び（1..40）、1 トランザクションで `WHERE id IN (?)` の検索を 10 回。テキストプロトコル（DSN に `interpolateParams=true`） |
| `sysbench-config-prepared.json` | 同じ内容を素朴版プリペアドで |
| `sysbench-config-prepared-multi.json` | 同じ内容を multi-statement で |
| `config.json` | `IN (?)` の配列パラメータの例いくつか（`sbtest1..3` に対して）。`array` 型の見本 |

これらはテーブル数 40、id の上限 500000 で、8.1 / 8.2 とは違います。

### 8.4 完全な例：sysbench-config-oltp-read-write-fixprepared.json

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
    { "sql": "", "comment": "接続ごとにテーブルを 1 つ選び（1..100）、以後ずっと使う",
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

（`comment` は一部省略しています。それ以外はリポジトリのファイルと同じです。）

## 9. OS チューニング（短い接続で高 QPS のとき）

`connection_type: "short"` ではトランザクションのたびに TCP 接続を新しく張るので、クライアント側の送信元ポートと TIME_WAIT がすぐにボトルネックになります。このツールを動かす Linux マシンで：

```bash
sysctl -w net.ipv4.ip_local_port_range="1024 65535"
sysctl -w net.ipv4.tcp_tw_reuse=1
sysctl -w net.ipv4.tcp_fin_timeout=10
sysctl -w net.netfilter.nf_conntrack_max=1048576
sysctl -w net.netfilter.nf_conntrack_buckets=262144

ulimit -n 1048576
echo "* soft nofile 1048576" >> /etc/security/limits.conf
echo "* hard nofile 1048576" >> /etc/security/limits.conf

# /etc/systemd/system.conf と /etc/systemd/user.conf に追記
DefaultLimitNOFILE=1048576
# systemd-logind を再起動
systemctl restart systemd-logind
```

短い接続のモードでは、ツール自身もソケットに `SO_REUSEADDR` と `SO_LINGER=0` を設定します（閉じるときに RST を送るので、クライアント側に TIME_WAIT が残りません）。`worker/dialer_base.go` を参照。
