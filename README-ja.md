# データベースワークロード・ジェネレーター

[English](README.md) | [中文文档](README-zh.md)

データベースのベンチマーク用の柔軟なワークロード生成ツールで、様々なデータ型と分布パターンをサポートします。

## 機能

- 複数のデータ型ジェネレーター：
  - 数値（一様/べき分布/分割分布）
  - 文字列（フォーマット数値、重み付け/一様集合）
  - 日付（カスタムフォーマット付きタイムスタンプ範囲）
  - 配列（設定可能な要素を持つ複合型）

- 設定可能なデータ分布：
  - 数値の一様分布
  - べき分布
  - 分割べき分布
  - 重み付けランダム選択
  - 時間範囲ベースの生成

## 使用方法

### 設定ファイル

`config.json`でワークロードを設定：

```json
{
  "concurrency": 10,
  "rate_per_thread": 100,
  "db_conn_str": "user:password@tcp(127.0.0.1:4000)/dbname?parseTime=true",
  "use_transaction": true,
  "connection_type": "long",
  "templates": [
    {
      "sql": "INSERT INTO users (id, name, created_at, tags) VALUES (?, ?, ?, ?)",
      "params": [
        {
          "type": "number",
          "random_mode": "uniform",
          "min": 1,
          "max": 1000000
        },
        {
          "type": "string",
          "random_mode": "number_format",
          "format": "user_%d",
          "number_config": {
            "random_mode": "uniform",
            "min": 1,
            "max": 1000
          }
        },
        {
          "type": "date",
          "random_mode": "range",
          "start": "2023-01-01T00:00:00Z",
          "end": "2023-12-31T23:59:59Z",
          "format": "2006-01-02 15:04:05"
        }
      ]
    },
    {
      "sql": "SELECT * FROM users WHERE id = ?",
      "params": [
        {
          "type": "number",
          "random_mode": "power_law",
          "min": 1,
          "max": 1000000,
          "exponent": 2.0
        }
      ]
    },
    {
      "sql": "SELECT * FROM users WHERE id = ?",
      "params": [
        {
          "type": "number",
          "random_mode": "partition_power_law",
          "min": 1,
          "max": 100000000,
          "exponent": 2.0,
          "partition": 2000
        }
      ]
    },
    {
      "sql": "SELECT * FROM users WHERE id in (?)",
      "params": [
        {
          "type": "array",
          "array_size": 4,
          "element_type": "number",
          "element_config": {
            "random_mode": "partition_power_law",
            "min": 1,
            "max": 100000000,
            "exponent": 1.001,
            "partition": 2000
          }
        }
      ]
    },
    {
      "sql": "SELECT * FROM sbtest1 WHERE c in (?)",
      "params": [
        {
          "type": "array",
          "array_size": 4,
          "element_type": "string",
          "element_config": {
            "type": "string",
            "random_mode": "number_format",
            "format": "abc_%d",
            "number_config": {
              "random_mode": "partition_power_law",
              "min": 1,
              "max": 100000000,
              "exponent": 1.001,
              "partition": 2000
            }
          }
        }
      ]
    }
  ]
}
```

### 実行コマンド

```bash
database_workload -config config.json
```

## パラメータ型リファレンス

### 1. 数値ジェネレーター

```json
{
  "type": "number",
  "random_mode": "uniform",
  "min": 1,
  "max": 1000000
}
```
```json
{
  "type": "number",
  "random_mode": "power_law",
  "min": 1,
  "max": 1000000,
  "exponent": 1.01  // for power_law distribution
}
```
```json
{
  "type": "number",
  "random_mode": "partition_power_law",
  "min": 1,
  "max": 1000000,
  "exponent": 1.01,  // for power_law distribution in one partition
  "partition": 100   // number of partitions
}
```


### 2. 文字列ジェネレーター

```json
{
  "type": "string",
  "random_mode": "set",         // "set"(セット), "number_format"(数値フォーマット)
  "set_mode": "weighted",       // "weighted"(重み付け), "uniform"(一様)
  "values": {
    "value1": 0.7,
    "value2": 0.3
  }
}
```
```json
{
  "type": "string",
  "random_mode": "number_format",
  "format": "abc_%d",
  "number_config": {
    "random_mode": "partition_power_law",
    "min": 1,
    "max": 100000000,
    "exponent": 1.001,
    "partition": 2000
  }
}
```

### 3. 日付ジェネレーター

```json
{
  "type": "date",
  "random_mode": "range",
  "start": "2023-01-01T00:00:00Z",
  "end": "2023-12-31T23:59:59Z",
  "format": "2006-01-02 15:04:05"
}
```

### 4. 配列ジェネレーター

```json
{
  "type": "array",
  "array_size": 4,
  "element_type": "number",
  "element_config": {
    "random_mode": "partition_power_law",
    "min": 1,
    "max": 100000000,
    "exponent": 1.001,
    "partition": 2000
  }
}
```

5. **配列ジェネレーター(ランダム数字から転換された文字列)**:
```json
{
    "type": "array",
    "array_size": 4,
    "element_type": "string",
    "element_config": {
        "type": "string",
        "random_mode": "number_format",
        "format": "abc_%d",
        "number_config": {
            "random_mode": "partition_power_law",
            "min": 1,
            "max": 100000000,
            "exponent": 1.001,
            "partition": 2000
        }
    }
}
```

## Sysbench OLTP 設定ファイル

このリポジトリには、すぐ使えるいくつかの`sysbench-config-*.json`ファイル（それに加えて、より古い`config.json`/`sysbench-config.json`）が同梱されており、それぞれ異なる実行パスを使うため、同じ並行数/スループットで互いに比較ベンチマークできます。「フル版」oltp-read-writeファミリー（4種類のSELECTバリエーション＋UPDATE×2＋DELETE＋INSERT、sysbench自身の`oltp_read_write.lua`を移植したもの）が、fix-prepared／multi-statement／pipelined-binaryの比較テストで実際に使われているものです。`*-prepared*`／`sysbench-config.json`／`config.json`は、もっと早い時期の小さなプロトタイプで、ローカルでの簡単な動作確認用に残してあるだけで、本格的な比較ベンチマークには使いません。

| ファイル | テンプレート | 実行モード | baselineに対して追加されるフラグ |
|---|---|---|---|
| `sysbench-config-oltp-read-write.json` | フル版oltp-read-write（10テンプレート） | 普通の`database/sql`prepared statement。`(*sql.Tx).StmtContext`経由——これが「素朴な」baselineで、実は毎回黙って再PREPAREしてしまう（`config/config.go`の`FixPreparedStatementReuse`のdocコメント参照）。そのためベンチマークで対照に使う「fix-prepared」baselineとは**別物**なので注意 | `use_prepared_statements:true`、`multi_statements:false` |
| `sysbench-config-oltp-read-write-fixprepared.json` | 同上 | **fix-prepared**：各ステートメントを一度だけPREPAREし、コネクションの寿命が続く限り使い回す（上記のstdlibの`Tx.StmtContext`再PREPARE問題を回避）——他のすべてのモードがこれと比較される基準 | `fix_prepared_statement_reuse:true`を追加 |
| `sysbench-config-oltp-read-write-multi.json` | 同上 | **multi-statement**：トランザクション全体を`tidb-multistmt`によるテキストプロトコルの1往復（`SET`マーカー＋`EXECUTE ps USING ...`）にまとめる | `multi_statements:true` |
| `sysbench-config-oltp-read-write-pipelined.json` | 同上 | **binary-multistmt（pipelined）**：トランザクション全体を`github.com/dulao5/tidb-binary-multistmt`経由でパイプライン化——バイナリの`COM_STMT_EXECUTE`パケットを連続して書き込み、1往復で済ませる。テキストプロトコルの`SET`マーカーは不要 | `pipelined_binary:true`を追加（`multi_statements`も一貫性のため設定してあるが、このモードでは無視される——`PipelinedBinary`のdocコメント参照） |
| `sysbench-config-prepared.json` | 最小限の2テンプレートのプロトタイプ（テーブル選択＋バッチpoint-selectのみ。フル版sysbench移植より前のもの） | 普通のprepared statement（上のフル版baselineと同じ注意点あり） | `use_prepared_statements:true`、`multi_statements:false` |
| `sysbench-config-prepared-multi.json` | 同じ最小限のテンプレート | multi-statementバッチ処理 | `multi_statements:true` |
| `sysbench-config.json` | 同じ最小限のテンプレート | 純粋なテキストプロトコルのクエリ、prepared statementは一切使わない | （なし） |
| `config.json` | `WHERE id IN (?)`の単一テンプレート | 純粋なテキストプロトコルのクエリ。上の「使い方」セクションの例で使われているファイルで、`array`型のパラメータジェネレーターのデモ | （なし） |

## OSチューニング（connection_typeが"short"の高QPSシナリオ向け）
```
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
# systemd-logind を再起動：
systemctl restart systemd-logind
```
