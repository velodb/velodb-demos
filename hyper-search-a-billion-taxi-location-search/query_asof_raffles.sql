-- Taxis within 500 m of Raffles Place MRT (1.2840, 103.8515) as of a point in time.
-- "As of T" = each taxi's last ping at or before T (pings are every ~30 s, so look back 2 minutes).
--   ts window            -> partition pruning (+ zone map on the sort key)
--   geohash6 IN (9 cells)-> bucket pruning + inverted index   (cell + 8 neighbours around Raffles Place)
--   st_distance_sphere   -> exact radius on the few survivors
SELECT taxi_id, plate, company, status, speed_kmh, lat, lon, ts AS last_seen,
       round(st_distance_sphere(lon, lat, 103.8515, 1.2840)) AS dist_m
FROM (
  SELECT *, row_number() OVER (PARTITION BY taxi_id ORDER BY ts DESC) AS rn
  FROM demo_hyper_search.taxi_ping
  WHERE ts >  '2026-08-26 13:35:38.917'
    AND ts <= '2026-08-26 13:37:38.917'
    AND geohash6 IN ('w21z6b','w21z6c','w21z6f','w21z70','w21z71','w21z72','w21z73','w21z74','w21z76')
) t
WHERE rn = 1
  AND st_distance_sphere(lon, lat, 103.8515, 1.2840) <= 500
ORDER BY dist_m;
