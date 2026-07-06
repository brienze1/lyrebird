Feature: Spy record and passthrough
  As an engineer or agent
  I want unmatched requests recorded and forwarded verbatim to a real upstream
  So that Lyrebird is a recording proxy I can point anything at (FR-001/002/003, SC-001)

  Background:
    Given a fresh temporary Lyrebird data directory

  Scenario: Upstream 2xx response is returned verbatim and recorded
    Given Lyrebird boots
    And a fake upstream server that responds 200 with body "hello" and header "X-Test: 1"
    And an upstream "example.local" configured in partition "default" pointing at the fake upstream
    When I send a GET request to "/anything" on the data plane with host "example.local"
    Then the response status is 200
    And the response body is "hello"
    And the response header "X-Test" is "1"
    And the recorded traffic for that request has decision "proxied"
    And the recorded traffic response body is "hello"
    And the recorded traffic response header "X-Test" is "1"

  Scenario: Upstream 5xx response is returned verbatim
    Given Lyrebird boots
    And a fake upstream server that responds 503 with body "upstream broken" and header "X-Test: 1"
    And an upstream "example.local" configured in partition "default" pointing at the fake upstream
    When I send a GET request to "/anything" on the data plane with host "example.local"
    Then the response status is 503
    And the response body is "upstream broken"
    And the response header "X-Test" is "1"
    And the recorded traffic for that request has decision "proxied"

  Scenario: Unreachable upstream synthesizes a 502
    Given Lyrebird boots
    And an upstream "down.local" configured in partition "default" pointing at a closed port
    When I send a GET request to "/anything" on the data plane with host "down.local"
    Then the response status is 502
    And the recorded traffic for that request has decision "proxied"

  Scenario: Upstream timeout synthesizes a 504
    Given the upstream timeout is configured to "200ms"
    And Lyrebird boots
    And a fake upstream server that hangs for "2s"
    And an upstream "slow.local" configured in partition "default" pointing at the fake upstream
    When I send a GET request to "/anything" on the data plane with host "slow.local"
    Then the response status is 504
    And the recorded traffic for that request has decision "proxied"

  Scenario: No upstream configured returns not_configured
    Given Lyrebird boots
    When I send a GET request to "/anything" on the data plane with host "unknown.local"
    Then the response status is 404
    And the response body contains "not_configured"
    And the recorded traffic for that request has decision "not_configured"

  Scenario: A body larger than the cap streams through fully but the recording is truncated
    Given the body cap is configured to "16" bytes
    And Lyrebird boots
    And a fake upstream server that echoes the request body it receives
    And an upstream "echo.local" configured in partition "default" pointing at the fake upstream
    When I send a POST request to "/anything" on the data plane with host "echo.local" and a body of 1000 bytes
    Then the response status is 200
    And the fake upstream received a body of 1000 bytes
    And the recorded traffic request body is truncated
    And the recorded traffic request body_total_size is 1000

  Scenario: An upstream declared in a seed file is resolvable for spy passthrough
    Given a seed file declares an upstream "seeded.local" in partition "default" pointing at a fake upstream
    And Lyrebird boots
    When I send a GET request to "/anything" on the data plane with host "seeded.local"
    Then the response status is 200
    And the response body is "seeded-upstream-response"
    And the recorded traffic for that request has decision "proxied"

  Scenario: Partition isolation for upstream resolution
    Given Lyrebird boots
    And an upstream "shared.local" configured in partition "team-a" pointing at a fake upstream
    When I send a GET request to "/anything" on the data plane with host "shared.local" in partition "team-a"
    Then the response status is 200
    And the recorded traffic for that request has decision "proxied"
    When I send a GET request to "/anything" on the data plane with host "shared.local" in partition "team-b"
    Then the response status is 404
    And the response body contains "not_configured"
