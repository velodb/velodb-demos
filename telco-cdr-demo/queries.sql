-- Telco CDR demo queries. Bill cycle = the 30 loaded days (see load_base.txt).
USE telco_cdr;

-- 1. Volume overview
SELECT 'call_log' t, COUNT(*) records, COUNT(DISTINCT msisdn) subscribers, MIN(start_time), MAX(start_time) FROM call_log
UNION ALL
SELECT 'data_stream_log', COUNT(*), COUNT(DISTINCT msisdn), MIN(start_time), MAX(start_time) FROM data_stream_log;

-- 2. Rate every voice / SMS CDR (per-record charge, before allowances)
--    voice billed per started minute; MT free unless roaming; toll-free and emergency free.
SELECT c.msisdn, c.start_time, c.call_type, c.direction, c.other_party_type, c.duration_sec,
       CASE
         WHEN c.call_type = 'SMS' THEN IF(c.direction = 'MO' AND c.call_result = 'DELIVERED',
                                          IF(c.roaming, 3 * p.sms_each, p.sms_each), 0)
         WHEN c.duration_sec = 0 OR c.other_party_type IN ('TOLLFREE', 'EMERGENCY') THEN 0
         WHEN c.roaming THEN CEIL(c.duration_sec / 60) * p.roaming_voice_per_min
         WHEN c.direction = 'MT' THEN 0
         WHEN c.other_party_type = 'INTERNATIONAL' THEN CEIL(c.duration_sec / 60) * p.intl_per_min
         WHEN c.other_party_type = 'PREMIUM' THEN CEIL(c.duration_sec / 60) * p.premium_per_min
         ELSE CEIL(c.duration_sec / 60) * p.voice_per_min
       END AS rated_charge
FROM call_log c JOIN rate_plan p ON c.rate_plan = p.rate_plan
WHERE c.msisdn = 6580000123
ORDER BY c.start_time;

-- 3. Monthly bill for one subscriber: allowances, overage, zero-rating, roaming
WITH p AS (SELECT * FROM rate_plan),
voice AS (
  SELECT msisdn, rate_plan,
    SUM(IF(call_type <> 'SMS' AND direction IN ('MO','FWD') AND NOT roaming
           AND other_party_type IN ('ONNET','OFFNET') AND duration_sec > 0, CEIL(duration_sec / 60), 0)) dom_min,
    SUM(IF(call_type <> 'SMS' AND direction IN ('MO','FWD') AND NOT roaming
           AND other_party_type = 'INTERNATIONAL', CEIL(duration_sec / 60), 0)) intl_min,
    SUM(IF(call_type <> 'SMS' AND direction IN ('MO','FWD') AND NOT roaming
           AND other_party_type = 'PREMIUM', CEIL(duration_sec / 60), 0)) premium_min,
    SUM(IF(call_type <> 'SMS' AND roaming, CEIL(duration_sec / 60), 0)) roam_min,
    SUM(IF(call_type = 'SMS' AND direction = 'MO' AND call_result = 'DELIVERED', 1, 0)) sms
  FROM call_log WHERE msisdn = 6580000123 GROUP BY msisdn, rate_plan
),
data AS (
  SELECT d.msisdn,
    SUM(IF(NOT d.roaming AND d.rating_group NOT IN (99, 50)
           AND NOT (d.rating_group = 20 AND p.video_zero_rated)
           AND NOT (d.rating_group = 30 AND p.social_zero_rated),
           d.uplink_bytes + d.downlink_bytes, 0)) / 1048576 dom_mb,
    SUM(IF(d.roaming, d.uplink_bytes + d.downlink_bytes, 0)) / 1048576 roam_mb,
    SUM(d.uplink_bytes + d.downlink_bytes) / 1048576 total_mb
  FROM data_stream_log d JOIN p ON d.rate_plan = p.rate_plan
  WHERE d.msisdn = 6580000123 GROUP BY d.msisdn
)
SELECT v.msisdn, v.rate_plan, p.monthly_fee,
  v.dom_min, p.included_voice_min, GREATEST(v.dom_min - p.included_voice_min, 0) * p.voice_per_min  AS voice_overage,
  v.intl_min * p.intl_per_min                                                                        AS intl_charge,
  v.premium_min * p.premium_per_min                                                                  AS premium_charge,
  v.roam_min * p.roaming_voice_per_min                                                               AS roam_voice_charge,
  v.sms, GREATEST(v.sms - p.included_sms, 0) * p.sms_each                                            AS sms_charge,
  ROUND(d.total_mb) total_mb, ROUND(d.dom_mb) chargeable_mb, p.included_data_mb,
  ROUND(GREATEST(d.dom_mb - p.included_data_mb, 0) * p.data_per_mb, 2)                               AS data_overage,
  ROUND(d.roam_mb * p.roaming_data_per_mb, 2)                                                        AS roam_data_charge
FROM voice v JOIN data d ON v.msisdn = d.msisdn JOIN p ON v.rate_plan = p.rate_plan;

-- 4. Bill run for the whole base: revenue by plan (full scan + join + aggregation over both tables)
WITH usage_v AS (
  SELECT msisdn, rate_plan,
    SUM(IF(call_type <> 'SMS' AND direction IN ('MO','FWD') AND NOT roaming AND other_party_type IN ('ONNET','OFFNET'),
           CEIL(duration_sec / 60), 0)) dom_min,
    SUM(IF(call_type <> 'SMS' AND direction IN ('MO','FWD') AND NOT roaming AND other_party_type = 'INTERNATIONAL',
           CEIL(duration_sec / 60), 0)) intl_min,
    SUM(IF(call_type <> 'SMS' AND roaming, CEIL(duration_sec / 60), 0)) roam_min
  FROM call_log GROUP BY msisdn, rate_plan
),
usage_d AS (
  SELECT d.msisdn, d.rate_plan,
    SUM(IF(NOT d.roaming AND d.rating_group NOT IN (99, 50)
           AND NOT (d.rating_group = 20 AND p.video_zero_rated)
           AND NOT (d.rating_group = 30 AND p.social_zero_rated), d.uplink_bytes + d.downlink_bytes, 0)) / 1048576 dom_mb,
    SUM(IF(d.roaming, d.uplink_bytes + d.downlink_bytes, 0)) / 1048576 roam_mb
  FROM data_stream_log d JOIN rate_plan p ON d.rate_plan = p.rate_plan
  GROUP BY d.msisdn, d.rate_plan
),
bill AS (
  SELECT COALESCE(v.msisdn, d.msisdn) msisdn, COALESCE(v.rate_plan, d.rate_plan) rate_plan,
    p.monthly_fee,
    GREATEST(COALESCE(v.dom_min, 0) - p.included_voice_min, 0) * p.voice_per_min
      + COALESCE(v.intl_min, 0) * p.intl_per_min + COALESCE(v.roam_min, 0) * p.roaming_voice_per_min AS voice_charge,
    GREATEST(COALESCE(d.dom_mb, 0) - p.included_data_mb, 0) * p.data_per_mb
      + COALESCE(d.roam_mb, 0) * p.roaming_data_per_mb AS data_charge
  FROM usage_v v FULL OUTER JOIN usage_d d ON v.msisdn = d.msisdn
  JOIN rate_plan p ON COALESCE(v.rate_plan, d.rate_plan) = p.rate_plan
)
SELECT rate_plan, COUNT(*) subscribers,
  ROUND(SUM(monthly_fee)) recurring, ROUND(SUM(voice_charge)) voice_usage, ROUND(SUM(data_charge)) data_usage,
  ROUND(SUM(monthly_fee + voice_charge + data_charge) / COUNT(*), 2) arpu
FROM bill GROUP BY rate_plan ORDER BY rate_plan;

-- 5. Bill-shock alert: roaming subscribers whose roaming data charge today crossed $50
SELECT d.msisdn, d.visited_plmn, ROUND(SUM(d.uplink_bytes + d.downlink_bytes) / 1048576) mb,
       ROUND(SUM(d.uplink_bytes + d.downlink_bytes) / 1048576 * MAX(p.roaming_data_per_mb), 2) charge
FROM data_stream_log d JOIN rate_plan p ON d.rate_plan = p.rate_plan
WHERE d.roaming AND d.start_time >= (SELECT DATE(MAX(start_time)) FROM data_stream_log)
GROUP BY d.msisdn, d.visited_plmn HAVING charge > 50 ORDER BY charge DESC LIMIT 20;

-- 6. Network: busy-hour traffic and drop-call rate per cell (inverted index / partition pruning)
SELECT cell_id, COUNT(*) calls,
       ROUND(100 * SUM(call_result = 'DROPPED') / SUM(call_result IN ('ANSWERED', 'DROPPED')), 2) drop_rate_pct,
       ROUND(SUM(duration_sec) / 3600, 1) erlang_hours
FROM call_log
WHERE start_time >= (SELECT DATE(MAX(start_time)) FROM call_log) AND call_type <> 'SMS'
GROUP BY cell_id ORDER BY drop_rate_pct DESC LIMIT 20;

-- 7. Data traffic by application and hour (last 7 days)
SELECT app_category, HOUR(start_time) hr, ROUND(SUM(uplink_bytes + downlink_bytes) / POW(1024, 4), 2) tib
FROM data_stream_log
WHERE start_time >= (SELECT DATE_SUB(DATE(MAX(start_time)), INTERVAL 6 DAY) FROM data_stream_log)
GROUP BY 1, 2 ORDER BY 1, 2;

-- 8. Lookup: every CDR involving a number (inverted index on other_party_number)
SELECT msisdn, start_time, call_type, direction, call_result, duration_sec
FROM call_log WHERE other_party_number = '6580000123' ORDER BY start_time LIMIT 50;
