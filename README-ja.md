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
  "random_mode": "uniform",     // "uniform"(一様), "power_law"(べき分布), "partitioned"(分割)
  "min": 1,
  "max": 1000000,
  "alpha": 2.0                  // べき分布用
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
