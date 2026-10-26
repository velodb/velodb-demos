select count(*) from log_events;

select * from log_events limit 5;



SELECT COUNT(*) AS error_events
from log_events
WHERE 
  host_id='host086'
  AND event_time >= '2026-10-01 04:00:00'
  AND event_time < '2026-10-01 12:00:00'
  AND payload['severity'] = 'error';



SELECT COUNT(*) AS matching_events
from log_events
WHERE event_time >= '2026-10-01 10:00:00'
  AND event_time < '2026-10-01 12:00:00'
  AND payload['country_code'] = 'US'
  AND payload['city'] = 'New York'
  AND payload['severity'] = 'error';



SELECT host_id, count(*) as dolore_count
from log_events
WHERE event_time >= '2026-10-01 08:00:00'
  AND event_time < '2026-10-01 12:00:00'
  and host_id like 'host00%'
  AND payload['message'] MATCH_PHRASE 'lorem dolore'
  AND payload['severity'] = 'error'
group by host_id
order by host_id
;
