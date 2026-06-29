# Infrastructure Load Testing


This directory contains multithreaded benchmarking scripts utilizing `wrk` to validate the throughput and latency bounds of the local infrastructure.

## Bifrost Benchmark (Local Execution)


**Hardware Profile:** Single Node / Localhost
**Concurrency:** 12 Threads, 100 Connections
**Duration:** 30 Seconds

### Results

| Metric | Measurement |
| :--- | :--- |
| **Throughput (RPS)** | 11,768.95 Req/Sec |
| **Data Transfer** | 861.98 KB/sec |
| **Average Latency** | 8.94 ms |
| **Max Latency** | 201.52 ms |


### Analysis
The sub-9ms average latency confirms the architectural thesis of the edge gateway. By deferring JSON deserialization to the background worker pool (Mimir) and collapsing deduplication and insertion into a single Redis Lua script execution, Bifrost successfully processes >11k events per second without CPU saturation or memory bloat.

### Running the Benchmark
Ensure the local cluster is running (`make up` and `make run-bifrost`), then execute:
```bash

make bench
```
