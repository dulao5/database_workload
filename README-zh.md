# 数据库工作负载生成器

[English](README.md) | [日本語](README-ja.md)

一个灵活的数据库基准测试工作负载生成器，支持多种数据类型和分布模式。

## 功能特性

- 多种数据类型生成器：
  - 数字（均匀/幂律/分区分布）
  - 字符串（格式化数字、加权/均匀集合）
  - 日期（带自定义格式的时间戳范围）
  - 数组（可配置元素的复合类型）

- 可配置的数据分布：
  - 数字的均匀分布
  - 幂律分布
  - 分区幂律
  - 加权随机选择
  - 基于时间范围的生成

## 使用方法

### 配置文件

在 `config.json` 中配置工作负载：

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

### 运行命令

```bash
database_workload -config config.json
```

## 参数类型参考

### 1. 数字生成器

```json
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


### 2. 字符串生成器

```json
{
  "type": "string",
  "random_mode": "set",         // "set"(集合), "number_format"(数字格式化)
  "set_mode": "weighted",       // "weighted"(加权), "uniform"(均匀)
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

### 3. 日期生成器

```json
{
  "type": "date",
  "random_mode": "range",
  "start": "2023-01-01T00:00:00Z",
  "end": "2023-12-31T23:59:59Z",
  "format": "2006-01-02 15:04:05"
}
```

### 4. 数组生成器

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
5. **数组生成器(随机数字的格式化字符串)**:
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