# Database Workload Generator

[中文文档](README-zh.md) | [日本語](README-ja.md)

A flexible workload generator for database benchmarking that supports various data types and distribution patterns.

## Features

- Multiple data type generators:
  - Numbers (uniform/power-law/partitioned distributions)
  - Strings (formatted numbers, weighted/uniform sets)
  - Dates (timestamp ranges with custom formatting)
  - Arrays (composite type with configurable elements)

- Configurable data distributions:
  - Uniform distribution for numbers
  - Power law distribution
  - Partitioned power law
  - Weighted random selection
  - Time range based generation

## Usage

Configure your workload in `config.json`:
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
        },
        {
          "type": "string",
          "random_mode": "number_format",
          "format": "tag_%d",
          "number_config": {
            "random_mode": "uniform",
            "min": 1,
            "max": 1000
          }
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

Usage:
```bash
database_workload -config config.json
```

### Example Parameter Types

1. **Number Generator**:
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

2. **String Generator**:
```json
{
  "type": "string",
  "random_mode": "set",         // "set", "number_format"
  "set_mode": "weighted",       // "weighted", "uniform"
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

3. **Date Generator**:
```json
{
  "type": "date",
  "random_mode": "range",
  "start": "2023-01-01T00:00:00Z",
  "end": "2023-12-31T23:59:59Z",
  "format": "2006-01-02 15:04:05"
}
```

4. **Array Generator**:
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

5. **Array Generator(formated string from random numbers)**:
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

## OS Tuning (for high QPS scenario when connection_type is "short")
```
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
# restart  systemd-logind：
systemctl restart systemd-logind
```
