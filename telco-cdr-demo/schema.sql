-- Telco CDR demo: voice/SMS call records + packet-data usage records (xDR), rated in Doris.
--
-- Created by run-load.sh --create, which fills __START_DATE__ / __END_DATE__ with the load window (3 months).
-- Partitions are fixed day ranges: no auto or dynamic partitioning, no retention, so the data never ages out.
--
-- Data per-MB rates assume ~15GB/subscriber/month of usage (3-month q3 dataset).
-- Subscriber base: 5,000,000 MSISDNs 6580000000..6584999999 (home PLMN 525-01).
-- Subscriber attributes (prepaid/postpaid, rate plan) are a pure function of the MSISDN,
-- so they are consistent across both tables and with the rate_plan tariff table.

CREATE DATABASE IF NOT EXISTS telco_cdr;
USE telco_cdr;

-- ---------------------------------------------------------------------------------------
-- call_log: one row per voice call / SMS attempt (MSC / IMS CSCF mediation output)
-- ---------------------------------------------------------------------------------------
DROP TABLE IF EXISTS call_log;
CREATE TABLE call_log (
  msisdn            BIGINT       NOT NULL COMMENT 'served (home) subscriber this record is billed to',
  start_time        DATETIME(3)  NOT NULL COMMENT 'seizure time (local)',
  call_id           VARCHAR(20)  NOT NULL COMMENT 'unique CDR id',
  end_time          DATETIME(3)  NOT NULL COMMENT 'release time = start + ring + talk',
  imsi              VARCHAR(15)  NOT NULL,
  other_party_number VARCHAR(20) NOT NULL COMMENT 'called number for MO/FWD, calling number for MT; E.164 without +',
  call_type         VARCHAR(8)   NOT NULL COMMENT 'VOLTE | VONR | CS | VIDEO | SMS',
  direction         VARCHAR(4)   NOT NULL COMMENT 'MO originating | MT terminating | FWD call forwarding',
  other_party_type  VARCHAR(16)  NOT NULL COMMENT 'ONNET | OFFNET | INTERNATIONAL | TOLLFREE | PREMIUM | EMERGENCY',
  dest_country_code SMALLINT     NOT NULL COMMENT 'other party country code (65 = domestic)',
  call_result       VARCHAR(10)  NOT NULL COMMENT 'ANSWERED | NO_ANSWER | BUSY | FAILED | DROPPED | DELIVERED',
  release_cause     SMALLINT     NOT NULL COMMENT 'Q.850 cause code',
  ring_duration_ms  INT          NOT NULL,
  duration_sec      INT          NOT NULL COMMENT 'conversation time; 0 when not connected / SMS',
  rat               VARCHAR(5)   NOT NULL COMMENT 'GSM | UMTS | LTE | NR',
  cell_id           INT          NOT NULL,
  tac               INT          NOT NULL,
  msc_id            VARCHAR(8)   NOT NULL,
  roaming           BOOLEAN      NOT NULL,
  visited_plmn      VARCHAR(6)   NOT NULL COMMENT 'MCCMNC of serving network; 52501 = home',
  subscriber_type   VARCHAR(8)   NOT NULL COMMENT 'PREPAID | POSTPAID',
  rate_plan         VARCHAR(16)  NOT NULL COMMENT 'FK rate_plan.rate_plan',
  INDEX idx_other_party  (other_party_number) USING INVERTED,
  INDEX idx_call_result   (call_result)   USING INVERTED,
  INDEX idx_cell_id       (cell_id)       USING INVERTED
)
ENGINE = OLAP
DUPLICATE KEY(msisdn, start_time)
COMMENT 'Voice / SMS call detail records'
PARTITION BY RANGE (start_time) (FROM ("__START_DATE__") TO ("__END_DATE__") INTERVAL 1 DAY)
DISTRIBUTED BY HASH(msisdn) BUCKETS 8
PROPERTIES (
  "replication_num" = "1",
  "compression" = "ZSTD"
);

-- ---------------------------------------------------------------------------------------
-- data_stream_log: one row per PGW/SMF usage record (per-app flow interval)
-- ---------------------------------------------------------------------------------------
DROP TABLE IF EXISTS data_stream_log;
CREATE TABLE data_stream_log (
  msisdn            BIGINT       NOT NULL,
  start_time        DATETIME(3)  NOT NULL,
  record_id         VARCHAR(20)  NOT NULL COMMENT 'unique usage record id',
  end_time          DATETIME(3)  NOT NULL,
  session_id        BIGINT       NOT NULL COMMENT 'charging id of the PDU session',
  imsi              VARCHAR(15)  NOT NULL,
  apn               VARCHAR(16)  NOT NULL COMMENT 'internet | ims | mms | iot.m2m | corp.vpn',
  rat               VARCHAR(5)   NOT NULL COMMENT 'UMTS | LTE | NR',
  cell_id           INT          NOT NULL,
  tac               INT          NOT NULL,
  ue_ip             IPV4         NOT NULL,
  app_name          VARCHAR(24)  NOT NULL COMMENT 'DPI-classified application',
  app_category      VARCHAR(16)  NOT NULL,
  protocol          VARCHAR(4)   NOT NULL COMMENT 'TCP | UDP | QUIC',
  uplink_bytes      BIGINT       NOT NULL,
  downlink_bytes    BIGINT       NOT NULL,
  duration_sec      INT          NOT NULL,
  qos_5qi           TINYINT      NOT NULL,
  rating_group      INT          NOT NULL COMMENT '10 general | 20 video | 30 social+messaging | 40 music | 50 mms | 99 zero-rated',
  termination_cause VARCHAR(16)  NOT NULL,
  roaming           BOOLEAN      NOT NULL,
  visited_plmn      VARCHAR(6)   NOT NULL,
  subscriber_type   VARCHAR(8)   NOT NULL,
  rate_plan         VARCHAR(16)  NOT NULL,
  INDEX idx_app_name     (app_name)     USING INVERTED,
  INDEX idx_app_category (app_category) USING INVERTED,
  INDEX idx_cell_id      (cell_id)      USING INVERTED
)
ENGINE = OLAP
DUPLICATE KEY(msisdn, start_time)
COMMENT 'Packet data usage records'
PARTITION BY RANGE (start_time) (FROM ("__START_DATE__") TO ("__END_DATE__") INTERVAL 1 DAY)
DISTRIBUTED BY HASH(msisdn) BUCKETS 8
PROPERTIES (
  "replication_num" = "1",
  "compression" = "ZSTD"
);

-- ---------------------------------------------------------------------------------------
-- rate_plan: tariffs used by the CDR rating queries (queries.sql)
-- ---------------------------------------------------------------------------------------
DROP TABLE IF EXISTS rate_plan;
CREATE TABLE rate_plan (
  rate_plan               VARCHAR(16)   NOT NULL,
  subscriber_type         VARCHAR(8)    NOT NULL,
  monthly_fee             DECIMAL(10,2) NOT NULL,
  included_voice_min      INT           NOT NULL COMMENT 'domestic minutes per bill cycle',
  included_sms            INT           NOT NULL,
  included_data_mb        BIGINT        NOT NULL,
  voice_per_min           DECIMAL(10,4) NOT NULL COMMENT 'domestic, after allowance, per started minute',
  intl_per_min            DECIMAL(10,4) NOT NULL,
  premium_per_min         DECIMAL(10,4) NOT NULL,
  roaming_voice_per_min   DECIMAL(10,4) NOT NULL,
  sms_each                DECIMAL(10,4) NOT NULL,
  data_per_mb             DECIMAL(10,4) NOT NULL COMMENT 'domestic, after allowance',
  roaming_data_per_mb     DECIMAL(10,4) NOT NULL,
  roaming_data_cap        DECIMAL(10,2) NOT NULL COMMENT 'bill-shock cap on roaming data charges per bill cycle',
  video_zero_rated        BOOLEAN       NOT NULL COMMENT 'rating group 20 not counted',
  social_zero_rated       BOOLEAN       NOT NULL COMMENT 'rating group 30 not counted'
)
UNIQUE KEY(rate_plan)
DISTRIBUTED BY HASH(rate_plan) BUCKETS 1
PROPERTIES ("replication_num" = "1");

INSERT INTO rate_plan VALUES
  ('PRE_BASIC',     'PREPAID',   0.00,    0,    0,      0, 0.1600, 1.2000, 2.5000, 3.5000, 0.0500, 0.0050, 15.0000, 100.00, false, false),
  ('PRE_DATA_PACK', 'PREPAID',  10.00,   30,   50,  15360, 0.1600, 1.2000, 2.5000, 3.5000, 0.0500, 0.0040, 15.0000, 100.00, false, true ),
  ('PRE_TOURIST',   'PREPAID',  15.00,  100,  100,  51200, 0.1200, 0.9000, 2.5000, 3.5000, 0.0500, 0.0030, 15.0000, 150.00, false, true ),
  ('POST_LITE_20',  'POSTPAID', 20.00,  150,  200,  10240, 0.0800, 0.8000, 2.0000, 2.0000, 0.0500, 0.0030,  8.0000, 200.00, false, false),
  ('POST_PLUS_40',  'POSTPAID', 40.00,  500, 1000,  51200, 0.0600, 0.6000, 2.0000, 1.5000, 0.0400, 0.0020,  5.0000, 300.00, false, true ),
  ('POST_MAX_80',   'POSTPAID', 80.00, 3000, 5000, 204800, 0.0500, 0.4000, 1.8000, 1.0000, 0.0300, 0.0010,  3.0000, 500.00, true,  true ),
  ('POST_BIZ_60',   'POSTPAID', 60.00, 2000, 2000, 102400, 0.0500, 0.3500, 1.8000, 0.8000, 0.0300, 0.0015,  2.5000, 500.00, false, false);
