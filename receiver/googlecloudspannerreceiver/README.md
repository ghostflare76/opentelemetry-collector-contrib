# Google Cloud Spanner Receiver

| Status                   |           |
| ------------------------ |-----------|
| Stability                | [beta]    |
| Supported pipeline types | metrics   |
| Distributions            | [contrib] |

Google Cloud Spanner enable you to investigate issues with your database
by exposing via [Total and Top N built-in tables](https://cloud.google.com/spanner/docs/introspection):
- Query statistics
- Read statistics
- Transaction statistics
- Lock statistics
- and others

_Note_: Total and Top N built-in tables are used with 1 minute statistics granularity.

The ultimate goal of Google Cloud Spanner Receiver is to collect and transform those statistics into metrics
that would be convenient for further analysis by users. 

Supported pipeline types: metrics

## Getting Started

The following configuration example is:

```yaml
receivers:
  googlecloudspanner:
    collection_interval: 60s
    top_metrics_query_max_rows: 100
    backfill_enabled: true
    cardinality_total_limit: 200000
    projects:
      - project_id: "spanner project 1"
        service_account_key: "path to spanner project 1 service account json key"
        instances:
          - instance_id: "id1"
            databases:
              - "db11"
              - "db12"
          - instance_id: "id2"
            databases:
              - "db21"
              - "db22"
      - project_id: "spanner project 2"
        service_account_key: "path to spanner project 2 service account json key"
        instances:
          - instance_id: "id3"
            databases:
              - "db31"
              - "db32"
          - instance_id: "id4"
            databases:
              - "db41"
              - "db42"
```

Brief description of configuration properties:
- **googlecloudspanner** - name of the Cloud Spanner Receiver related section in OpenTelemetry collector configuration file
- **collection_interval** - this receiver runs periodically. Each time it runs, it queries Google Cloud Spanner, creates metrics, and sends them to the next consumer (default: 1 minute). **It is not recommended to change the default value of collection interval, since new values for metrics in the Spanner database appear only once a minute.**
- **top_metrics_query_max_rows** - max number of rows to fetch from Top N built-in table(100 by default)
- **backfill_enabled** - turn on/off 1-hour data backfill(by default it is turned off)
- **cardinality_total_limit** - limit of active series per 24 hours period. If specified, turns on cardinality filtering and handling. If zero or not specified, cardinality is not handled. You can read [this document](cardinality.md) for more information about cardinality handling and filtering.
- **projects** - list of GCP projects
    - **project_id** - identifier of GCP project
    - **service_account_key** - path to service account JSON key It is highly recommended to set this property to the correct value. In case it is empty, the [Application Default Credentials](https://google.aip.dev/auth/4110) will be used for the database connection.
    - **instances** - list of Google Cloud Spanner instance for connection
        - **instance_id** - identifier of Google Cloud Spanner instance
        - **databases** - list of databases used from this instance

[beta]: https://github.com/open-telemetry/opentelemetry-collector#beta
[contrib]: https://github.com/open-telemetry/opentelemetry-collector-releases/tree/main/distributions/otelcol-contrib