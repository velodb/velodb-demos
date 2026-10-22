-- Layout of the generated data_stream_log Parquet files (NOT a Doris table).
-- Columns are raw random draws; load.sh turns them into data_stream_log columns with SET expressions.
CREATE TABLE telco_cdr.data_stream_log_raw (
  record_id    VARCHAR(20) NOT NULL COMMENT 'unique id',
  session_id   BIGINT      NOT NULL,
  day_off      TINYINT     NOT NULL COMMENT '0..89: day slot, stretched over the 3 load months by run-load.sh',
  hour_of_day  TINYINT     NOT NULL COMMENT 'diurnal-weighted hour',
  ms_of_hour   INT         NOT NULL COMMENT '0..3599999',
  sub_u        INT         NOT NULL COMMENT '0..99999999, squared at load for skewed subscriber activity',
  apn          VARCHAR(16) NOT NULL,
  rat          VARCHAR(5)  NOT NULL,
  cell_u       INT         NOT NULL COMMENT '0..29999',
  ue_ip        IPV4        NOT NULL,
  app_raw      VARCHAR(24) NOT NULL,
  protocol     VARCHAR(4)  NOT NULL,
  vol_u        INT         NOT NULL COMMENT '0..999999, log-uniform volume at load',
  ul_u         INT         NOT NULL COMMENT '0..999, uplink ratio at load',
  dur_u        INT         NOT NULL COMMENT '0..999999, log-uniform bitrate at load (duration = volume / bitrate)',
  term_raw     VARCHAR(16) NOT NULL COMMENT 'TIME_LIMIT is set at load when duration hits 3600s'
) DUPLICATE KEY(record_id) DISTRIBUTED BY HASH(record_id) BUCKETS 1;
