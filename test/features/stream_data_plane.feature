Feature: Generic byte-stream data plane
  As an agent that needs to substitute a peripheral speaking framed bytes
  I want Lyrebird to serve matched mocks over a byte stream using the same match→respond model
  So that any framed protocol is mockable with no protocol code in Lyrebird (FR-001..FR-035)

  Background:
    Given a fresh temporary Lyrebird data directory
    And the byte-stream data plane is enabled
    And Lyrebird boots

  # ---------------------------------------------------------------- US1 (P1)

  Scenario: A matched frame is answered with the declared frame
    Given a stream endpoint "widget" delimited by CRLF
    And a stream mock "widget-read" for "/widget" responding with:
      """
      [{"text":"VALUE,7"}]
      """
    When a stand-in connects to endpoint "widget"
    Then the handshake is accepted
    When the stand-in sends the frame "READ,7"
    Then the stand-in receives the frame "VALUE,7"

  Scenario: A rule matches on a projected field rather than the whole frame
    Given a stream endpoint "widget" delimited by CRLF splitting on ","
    And a stream mock "widget-read" for "/widget" matching "$.fields.0" equal to "READ" responding with:
      """
      [{"text":"MATCHED"}]
      """
    When a stand-in connects to endpoint "widget"
    And the stand-in sends the frame "READ,7"
    Then the stand-in receives the frame "MATCHED"
    When the stand-in sends the frame "WRITE,7"
    Then the stand-in receives nothing

  Scenario: A rule's own projection overrides the endpoint's default
    Given a stream endpoint "widget" delimited by CRLF splitting on ","
    And a stream mock "by-offset" for "/widget" matching "$.at.kind" equal to "W" responding with:
      """
      [{"text":"BY-OFFSET"}]
      """
    And the stream mock "by-offset" projects "kind" at offset 0 length 1 as "text"
    When a stand-in connects to endpoint "widget"
    And the stand-in sends the frame "WRITE,7"
    Then the stand-in receives the frame "BY-OFFSET"

  Scenario: An answer copies bytes out of the frame that triggered it
    Given a stream endpoint "widget" delimited by CRLF splitting on ","
    And a stream mock "widget-echo" for "/widget" responding with:
      """
      [{"text":"VALUE,"},{"copyFrom":"$.fields.1"}]
      """
    When a stand-in connects to endpoint "widget"
    And the stand-in sends the frame "READ,42"
    Then the stand-in receives the frame "VALUE,42"

  Scenario: An unmatched frame is silent, recorded, and leaves the connection usable
    Given a stream endpoint "widget" delimited by CRLF
    And a stream mock "widget-read" for "/widget" matching "$.text" equal to "READ" responding with:
      """
      [{"text":"OK"}]
      """
    When a stand-in connects to endpoint "widget"
    And the stand-in sends the frame "NOTHING-MATCHES-THIS"
    Then the stand-in receives nothing
    And the traffic log has an entry for "/widget" with decision "not_configured"
    When the stand-in sends the frame "READ"
    Then the stand-in receives the frame "OK"

  Scenario: A stand-in naming an undeclared endpoint is refused by name
    When a stand-in connects to endpoint "nosuch"
    Then the handshake is refused with "ERR unknown endpoint nosuch"

  Scenario: A second stand-in claiming an occupied endpoint is refused
    Given a stream endpoint "widget" delimited by CRLF
    When a stand-in connects to endpoint "widget"
    Then the handshake is accepted
    When a second stand-in connects to endpoint "widget"
    Then the second handshake is refused with "ERR endpoint widget already connected"

  Scenario: A respond script builds the frame
    Given a stream endpoint "widget" delimited by CRLF splitting on ","
    And a stream mock "scripted" for "/widget" responding with the script:
      """
      '[{"text":"SCRIPTED"}]'
      """
    When a stand-in connects to endpoint "widget"
    And the stand-in sends the frame "READ,7"
    Then the stand-in receives the frame "SCRIPTED"

  # ---------------------------------------------------------------- US2 (P2)

  Scenario: A cadence emits its sequence in order with nothing having asked
    Given a stream endpoint "ticker" delimited by CRLF emitting every "40ms":
      """
      ["[{\"text\":\"TICK,0001\"}]", "[{\"text\":\"TICK,0002\"}]"]
      """
    When a stand-in connects to endpoint "ticker"
    Then the stand-in receives the frame "TICK,0001"
    And the stand-in receives the frame "TICK,0002"

  Scenario: An exhausted cadence repeats its last frame by default
    Given a stream endpoint "ticker" delimited by CRLF emitting every "40ms":
      """
      ["[{\"text\":\"FIRST\"}]", "[{\"text\":\"LAST\"}]"]
      """
    When a stand-in connects to endpoint "ticker"
    Then the stand-in receives the frame "FIRST"
    And the stand-in receives the frame "LAST"
    And the stand-in receives the frame "LAST"

  Scenario: An injected frame is delivered whole and recorded as unprompted
    Given a stream endpoint "widget" delimited by CRLF
    When a stand-in connects to endpoint "widget"
    And I inject the frame "[{\"text\":\"PUSHED\"}]" into endpoint "widget"
    Then the stand-in receives the frame "PUSHED"
    And the traffic log has an entry for "/widget" with method "EMIT"

  Scenario: Injecting with no stand-in connected reports it plainly
    Given a stream endpoint "widget" delimited by CRLF
    When I inject the frame "[{\"text\":\"PUSHED\"}]" into endpoint "widget"
    Then the injection is rejected

  Scenario: A frame the endpoint does not answer is absorbed and recorded
    Given a stream endpoint "widget" delimited by CRLF
    When a stand-in connects to endpoint "widget"
    And the stand-in sends the frame "SETUP-COMMAND"
    Then the stand-in receives nothing
    And the traffic log has an entry for "/widget" with method "IN"

  # ---------------------------------------------------------------- US3 (P3)

  Scenario: A delay fault answers late — the line is slow, not broken
    Given a stream endpoint "widget" delimited by CRLF
    And a stream mock "slow" for "/widget" faulting with "delay" of "120" ms
    When a stand-in connects to endpoint "widget"
    And the stand-in sends the frame "READ"
    Then the stand-in receives the frame "" after at least "100ms"
    And the traffic log has an entry for "/widget" with decision "faulted"

  Scenario: A reset fault drops the connection and a reconnect is served normally
    Given a stream endpoint "widget" delimited by CRLF
    And a stream mock "drop" for "/widget" faulting with "reset"
    When a stand-in connects to endpoint "widget"
    And the stand-in sends the frame "READ"
    Then the stand-in observes the connection closing
    When a stand-in connects to endpoint "widget"
    Then the handshake is accepted

  Scenario: A timeout fault writes nothing but is recorded distinctly from no match
    Given a stream endpoint "widget" delimited by CRLF
    And a stream mock "silent" for "/widget" faulting with "timeout"
    When a stand-in connects to endpoint "widget"
    And the stand-in sends the frame "READ"
    Then the stand-in receives nothing
    And the traffic log has an entry for "/widget" with decision "faulted"

  Scenario: A malformed fault answers without the framing terminator
    Given a stream endpoint "widget" delimited by CRLF
    And a stream mock "garbled" for "/widget" faulting with "malformed"
    When a stand-in connects to endpoint "widget"
    And the stand-in sends the frame "READ"
    Then the stand-in receives the unterminated bytes "not a valid frame"

  # ---------------------------------------------------------------- US4 (P4)

  Scenario: Both directions are recorded with the rule that answered
    Given a stream endpoint "widget" delimited by CRLF
    And a stream mock "widget-read" for "/widget" responding with:
      """
      [{"text":"OK"}]
      """
    When a stand-in connects to endpoint "widget"
    And the stand-in sends the frame "READ"
    Then the stand-in receives the frame "OK"
    And the traffic log has an entry for "/widget" with method "IN"
    And the traffic log has an entry for "/widget" with method "OUT"
    And the traffic log has an entry for "/widget" with decision "mocked"

  Scenario: A captured exchange is promotable into a rule that reproduces it
    Given a stream endpoint "widget" delimited by CRLF
    And a stream mock "widget-read" for "/widget" responding with:
      """
      [{"text":"OK"}]
      """
    When a stand-in connects to endpoint "widget"
    And the stand-in sends the frame "READ"
    Then the stand-in receives the frame "OK"
    When I promote the recorded "OUT" frame for "/widget"
    Then the promoted mock exists

  Scenario: An export carrying a promoted mock warns about captured traffic
    Given a stream endpoint "widget" delimited by CRLF
    And a stream mock "widget-read" for "/widget" responding with:
      """
      [{"text":"OK"}]
      """
    When a stand-in connects to endpoint "widget"
    And the stand-in sends the frame "READ"
    Then the stand-in receives the frame "OK"
    When I promote the recorded "OUT" frame for "/widget"
    Then the exported config warns about captured traffic

  Scenario: An export renders the endpoint so it can be re-seeded
    Given a stream endpoint "widget" delimited by CRLF splitting on ","
    Then the exported config contains the endpoint "widget"

  Scenario: A reset drops the connection, clears session state and keeps seeded
    Given a seeded stream endpoint "seeded-widget" delimited by CRLF
    And Lyrebird boots again
    And a stream endpoint "widget" delimited by CRLF
    When a stand-in connects to endpoint "widget"
    And I reset the space "default"
    Then the stand-in observes the connection closing
    When a stand-in connects to endpoint "widget"
    Then the handshake is refused with "ERR unknown endpoint widget"
    When a stand-in connects to endpoint "seeded-widget"
    Then the handshake is accepted

  Scenario: Two spaces with the same endpoint name do not observe each other
    Given a stream endpoint "widget" delimited by CRLF
    And a stream mock "default-rule" for "/widget" responding with:
      """
      [{"text":"FROM-DEFAULT"}]
      """
    And a stream endpoint "widget" delimited by CRLF in space "other"
    And a stream mock "other-rule" for "/widget" in space "other" responding with:
      """
      [{"text":"FROM-OTHER"}]
      """
    When a stand-in connects to endpoint "widget" in space "other"
    And the stand-in sends the frame "READ"
    Then the stand-in receives the frame "FROM-OTHER"

  # ------------------------------------------------------- bounds & parity

  Scenario: A proxy rule on a stream endpoint is refused when it is created
    Then creating a stream mock for "/widget" with a proxy action is rejected

  Scenario: Bytes that never complete a frame are abandoned and the next frame is served
    Given the body cap is configured to "64" bytes
    And Lyrebird boots again
    And a stream endpoint "widget" delimited by CRLF
    And a stream mock "widget-read" for "/widget" responding with:
      """
      [{"text":"OK"}]
      """
    When a stand-in connects to endpoint "widget"
    And the stand-in sends "300" unterminated bytes
    And the stand-in sends the frame "READ"
    Then the stand-in receives the frame "OK"

  Scenario: An endpoint listing reports occupancy
    Given a stream endpoint "widget" delimited by CRLF
    When a stand-in connects to endpoint "widget"
    Then the endpoint listing reports "widget" as occupied
