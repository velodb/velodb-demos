CREATE TABLE `dbo_invoiceevent_state` (
  `invoiceid` varchar(256) NULL,
  `eventdate` datetime(6) NULL,
  `organisationid` varchar(256) NULL,
  `eventid` varchar(256) NULL,
  `xc_cdc_operation` varchar(6) NULL,
  `xc_start_date_utc` datetime(6) NULL,
  `xc_shard_id` bigint NULL,
  `xc_etl_date_utc` datetime(6) NULL,
  `sequence` bigint NULL,
  `amount` decimal(19,4) NULL,
  `eventtypecode` varchar(150) NULL,
  `eventstatuscode` varchar(150) NULL
) ENGINE=OLAP
UNIQUE KEY(`invoiceid`, `eventdate`, `organisationid`, `eventid`)
AUTO PARTITION BY RANGE (date_trunc(`eventdate`, 'week'))()
DISTRIBUTED BY HASH(`organisationid`) BUCKETS 8
PROPERTIES (
    "replication_allocation" = "tag.location.default: 3",
    "enable_unique_key_merge_on_write" = "true",
    "function_column.sequence_col" = "xc_start_date_utc",
    "light_schema_change" = "true",
    "group_commit_interval_ms" = "10000",
    "group_commit_data_bytes" = "134217728"
);
