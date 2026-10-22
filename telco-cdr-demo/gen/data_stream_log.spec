version: 1

# Raw draws for gen/data_stream_log_raw.sql. load.sh derives the real usage-record columns.
batch:
  rows: 32768

fields:
  - name: record_id
    gen:
      type: sequence_string
      template: "D{}"
      width: 20

  - name: session_id
    gen:
      type: int_range
      min: 1000000000
      max: 999999999999

  - name: day_off
    gen:
      type: int_range
      min: 0
      max: 89

  - name: hour_of_day
    gen:
      type: weighted_choice
      values:
        - value: 0
          weight: 3.5
        - value: 1
          weight: 2.2
        - value: 2
          weight: 1.4
        - value: 3
          weight: 1.0
        - value: 4
          weight: 0.8
        - value: 5
          weight: 0.9
        - value: 6
          weight: 1.6
        - value: 7
          weight: 3.0
        - value: 8
          weight: 4.2
        - value: 9
          weight: 4.5
        - value: 10
          weight: 4.6
        - value: 11
          weight: 4.8
        - value: 12
          weight: 5.2
        - value: 13
          weight: 5.0
        - value: 14
          weight: 4.8
        - value: 15
          weight: 4.8
        - value: 16
          weight: 5.0
        - value: 17
          weight: 5.4
        - value: 18
          weight: 5.8
        - value: 19
          weight: 6.4
        - value: 20
          weight: 7.0
        - value: 21
          weight: 7.4
        - value: 22
          weight: 6.8
        - value: 23
          weight: 5.2

  - name: ms_of_hour
    gen:
      type: int_range
      min: 0
      max: 3599999

  - name: sub_u
    gen:
      type: int_range
      min: 0
      max: 99999999

  - name: apn
    gen:
      type: weighted_choice
      values:
        - value: "internet"
          weight: 90
        - value: "ims"
          weight: 4
        - value: "mms"
          weight: 0.5
        - value: "iot.m2m"
          weight: 3
        - value: "corp.vpn"
          weight: 2.5

  - name: rat
    gen:
      type: weighted_choice
      values:
        - value: "NR"
          weight: 46
        - value: "LTE"
          weight: 50
        - value: "UMTS"
          weight: 4

  - name: cell_u
    gen:
      type: int_range
      min: 0
      max: 29999

  - name: ue_ip
    gen:
      type: ipv4
      cidr: "10.64.0.0/10"

  - name: app_raw
    gen:
      type: weighted_choice
      values:
        - value: "YouTube"
          weight: 9
        - value: "Netflix"
          weight: 4
        - value: "Disney+"
          weight: 1.5
        - value: "Twitch"
          weight: 0.8
        - value: "TikTok"
          weight: 10
        - value: "Instagram"
          weight: 8
        - value: "Facebook"
          weight: 7
        - value: "X"
          weight: 2
        - value: "WhatsApp"
          weight: 9
        - value: "Telegram"
          weight: 3
        - value: "WeChat"
          weight: 3
        - value: "Spotify"
          weight: 4
        - value: "Apple Music"
          weight: 1.2
        - value: "Web Browsing"
          weight: 12
        - value: "Google Search"
          weight: 5
        - value: "Mobile Legends"
          weight: 3
        - value: "PUBG Mobile"
          weight: 1.8
        - value: "Genshin Impact"
          weight: 1
        - value: "Zoom"
          weight: 1.6
        - value: "Microsoft Teams"
          weight: 1.8
        - value: "iCloud"
          weight: 2
        - value: "Google Drive"
          weight: 1.4
        - value: "App Store"
          weight: 1.3
        - value: "Google Play"
          weight: 1.5
        - value: "Shopee"
          weight: 4
        - value: "Grab"
          weight: 3
        - value: "Unclassified"
          weight: 4

  - name: protocol
    gen:
      type: weighted_choice
      values:
        - value: "QUIC"
          weight: 38
        - value: "TCP"
          weight: 52
        - value: "UDP"
          weight: 10

  - name: vol_u
    gen:
      type: int_range
      min: 0
      max: 999999

  - name: ul_u
    gen:
      type: int_range
      min: 0
      max: 999

  - name: dur_u
    gen:
      type: int_range
      min: 0
      max: 999999

  - name: term_raw
    gen:
      type: weighted_choice
      values:
        - value: "NORMAL"
          weight: 75
        - value: "VOLUME_LIMIT"
          weight: 10
        - value: "IDLE_TIMEOUT"
          weight: 7
        - value: "RAT_CHANGE"
          weight: 6
        - value: "ABNORMAL"
          weight: 2
