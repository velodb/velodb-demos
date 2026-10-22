version: 1

# Raw draws for gen/call_log_raw.sql. load.sh derives the real CDR columns.
# Every column is independent, so generation runs on every CPU thread.
batch:
  rows: 32768

fields:
  - name: call_id
    gen:
      type: sequence_string
      template: "C{}"
      width: 20

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
          weight: 1.0
        - value: 1
          weight: 0.6
        - value: 2
          weight: 0.4
        - value: 3
          weight: 0.3
        - value: 4
          weight: 0.3
        - value: 5
          weight: 0.5
        - value: 6
          weight: 1.2
        - value: 7
          weight: 2.5
        - value: 8
          weight: 4.0
        - value: 9
          weight: 5.5
        - value: 10
          weight: 6.0
        - value: 11
          weight: 6.0
        - value: 12
          weight: 5.5
        - value: 13
          weight: 5.5
        - value: 14
          weight: 6.0
        - value: 15
          weight: 6.0
        - value: 16
          weight: 6.0
        - value: 17
          weight: 6.5
        - value: 18
          weight: 6.0
        - value: 19
          weight: 5.0
        - value: 20
          weight: 4.5
        - value: 21
          weight: 4.0
        - value: 22
          weight: 3.0
        - value: 23
          weight: 2.0

  - name: ms_of_hour
    gen:
      type: int_range
      min: 0
      max: 3599999

  - name: caller_u
    gen:
      type: int_range
      min: 0
      max: 99999999

  - name: call_type
    gen:
      type: weighted_choice
      values:
        - value: "VOLTE"
          weight: 62
        - value: "VONR"
          weight: 12
        - value: "CS"
          weight: 8
        - value: "VIDEO"
          weight: 3
        - value: "SMS"
          weight: 15

  - name: direction
    gen:
      type: weighted_choice
      values:
        - value: "MO"
          weight: 52
        - value: "MT"
          weight: 46
        - value: "FWD"
          weight: 2

  - name: other_party_type
    gen:
      type: weighted_choice
      values:
        - value: "ONNET"
          weight: 52
        - value: "OFFNET"
          weight: 36
        - value: "INTERNATIONAL"
          weight: 8
        - value: "TOLLFREE"
          weight: 2.5
        - value: "PREMIUM"
          weight: 1.4
        - value: "EMERGENCY"
          weight: 0.1

  - name: callee_rand
    gen:
      type: int_range
      min: 0
      max: 999999999

  - name: dest_cc
    gen:
      type: weighted_choice
      values:
        - value: 60
          weight: 22
        - value: 62
          weight: 14
        - value: 91
          weight: 16
        - value: 86
          weight: 12
        - value: 63
          weight: 10
        - value: 84
          weight: 5
        - value: 66
          weight: 4
        - value: 1
          weight: 6
        - value: 44
          weight: 4
        - value: 61
          weight: 4
        - value: 81
          weight: 3

  - name: result_raw
    gen:
      type: weighted_choice
      values:
        - value: "ANSWERED"
          weight: 79
        - value: "NO_ANSWER"
          weight: 9
        - value: "BUSY"
          weight: 4.5
        - value: "FAILED"
          weight: 4
        - value: "DROPPED"
          weight: 3.5

  - name: dur_u
    gen:
      type: int_range
      min: 0
      max: 999999

  - name: ring_ms
    gen:
      type: int_range
      min: 1200
      max: 22000

  - name: cell_u
    gen:
      type: int_range
      min: 0
      max: 29999

