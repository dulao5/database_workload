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

## Sysbench OLTP config files

The repo ships several ready-made `sysbench-config-*.json` files (plus the
older `config.json`/`sysbench-config.json`), each exercising a different
execution path so they can be benchmarked against each other at the same
concurrency/throughput. The "full" oltp-read-write family (all four SELECT
variants + 2 UPDATEs + DELETE + INSERT, ported from sysbench's own
`oltp_read_write.lua`) is the one actually used in every fix-prepared /
multi-statement / pipelined-binary comparison run; the `*-prepared*`/
`sysbench-config.json`/`config.json` files are earlier, smaller prototypes
kept around for quick local smoke-testing, not for head-to-head benchmarks.

| File | Templates | Execution mode | Flags beyond the baseline |
|---|---|---|---|
| `sysbench-config-oltp-read-write.json` | full oltp-read-write (10 templates) | plain `database/sql` prepared statements via `(*sql.Tx).StmtContext` — this is the "naive" baseline, and it silently re-PREPAREs every call (see `FixPreparedStatementReuse`'s doc comment in `config/config.go`), so it's *not* the same as the "fix-prepared" baseline used in benchmarks | `use_prepared_statements:true`, `multi_statements:false` |
| `sysbench-config-oltp-read-write-fixprepared.json` | same | **fix-prepared**: each statement is PREPAREd once and reused for the connection's lifetime (works around the stdlib `Tx.StmtContext` re-PREPARE behavior above) — this is the baseline every other mode gets compared against | adds `fix_prepared_statement_reuse:true` |
| `sysbench-config-oltp-read-write-multi.json` | same | **multi-statement**: the whole transaction is batched into one `tidb-multistmt` round trip over the text protocol (`SET` markers + `EXECUTE ps USING ...`) | `multi_statements:true` |
| `sysbench-config-oltp-read-write-pipelined.json` | same | **binary-multistmt (pipelined)**: the whole transaction is pipelined via `github.com/dulao5/tidb-binary-multistmt` — binary `COM_STMT_EXECUTE` packets written back-to-back, one round trip, no text-protocol `SET` markers | adds `pipelined_binary:true` (`multi_statements` is set for consistency but ignored in this mode — see `PipelinedBinary`'s doc comment) |
| `sysbench-config-prepared.json` | minimal 2-template prototype (table pick + batched point-selects only, predates the full sysbench port) | plain prepared statements (same caveat as the full baseline above) | `use_prepared_statements:true`, `multi_statements:false` |
| `sysbench-config-prepared-multi.json` | same minimal templates | multi-statement batching | `multi_statements:true` |
| `sysbench-config.json` | same minimal templates | plain text-protocol queries, no prepared statements at all | (none) |
| `config.json` | single `WHERE id IN (?)` template | plain text-protocol query; this is the file the Usage example above documents, demonstrating the `array`-type param generator | (none) |

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
