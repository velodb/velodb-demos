-- Layout of the generated call_log Parquet files (NOT a Doris table).
-- Columns are raw random draws; load.sh turns them into call_log columns with SET expressions.
CREATE TABLE telco_cdr.call_log_raw (
  call_id      VARCHAR(20) NOT NULL COMMENT 'unique id',
  day_off      TINYINT     NOT NULL COMMENT '0..89: day slot, stretched over the 3 load months by run-load.sh',
  hour_of_day  TINYINT     NOT NULL COMMENT 'diurnal-weighted hour',
  ms_of_hour   INT         NOT NULL COMMENT '0..3599999',
  caller_u     INT         NOT NULL COMMENT '0..99999999, squared at load for skewed subscriber activity',
  call_type    VARCHAR(8)  NOT NULL,
  direction    VARCHAR(4)  NOT NULL,
  other_party_type VARCHAR(16) NOT NULL,
  callee_rand  INT         NOT NULL COMMENT 'digits for the other-party number',
  dest_cc      SMALLINT    NOT NULL COMMENT 'country code, used when other_party_type = INTERNATIONAL',
  result_raw   VARCHAR(10) NOT NULL,
  dur_u        INT         NOT NULL COMMENT '0..999999, exponential duration at load',
  ring_ms      INT         NOT NULL,
  cell_u       INT         NOT NULL COMMENT '0..29999'
) DUPLICATE KEY(call_id) DISTRIBUTED BY HASH(call_id) BUCKETS 1;
