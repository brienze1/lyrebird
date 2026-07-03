Feature: Mock overrides
  As an engineer or agent
  I want declarative mocks to intercept matching calls while everything else still spies through
  So that I can override selected behavior without losing passthrough for the rest (FR-007/008/009/009a/010/011/013/025, SC-003)

  Background:
    Given a fresh temporary Lyrebird data directory

  Scenario: A matching mock responds without calling the upstream
    Given Lyrebird boots
    And an upstream "example.local" configured in partition "default" pointing at a fake upstream
    And a mock named "ping" matching GET path "/ping" that responds 200 with body "pong"
    When I send a GET request to "/ping" on the data plane with host "example.local"
    Then the response status is 200
    And the response body is "pong"
    And the fake upstream received 0 requests
    And the recorded traffic for that request has decision "mocked"

  Scenario: Higher-priority mock wins on a shared route
    Given Lyrebird boots
    And an upstream "example.local" configured in partition "default" pointing at a fake upstream
    And a mock named "low" with priority 1 matching GET path "/ping" that responds 200 with body "low"
    And a mock named "high" with priority 10 matching GET path "/ping" that responds 200 with body "high"
    When I send a GET request to "/ping" on the data plane with host "example.local"
    Then the response status is 200
    And the response body is "high"

  Scenario: Equal-priority mocks resolve to the most-recently-created
    Given Lyrebird boots
    And an upstream "example.local" configured in partition "default" pointing at a fake upstream
    And a mock named "first" with priority 5 matching GET path "/ping" that responds 200 with body "first"
    And a mock named "second" with priority 5 matching GET path "/ping" that responds 200 with body "second"
    When I send a GET request to "/ping" on the data plane with host "example.local"
    Then the response status is 200
    And the response body is "second"

  Scenario: A header-conditioned mock only fires on a matching header
    Given Lyrebird boots
    And an upstream "example.local" configured in partition "default" pointing at a fake upstream
    And a mock named "vip" matching GET path "/greet" with header "X-VIP" equals "1" that responds 200 with body "vip"
    When I send a GET request to "/greet" on the data plane with host "example.local" and header "X-VIP: 1"
    Then the response status is 200
    And the response body is "vip"
    When I send a GET request to "/greet" on the data plane with host "example.local"
    Then the response status is 200
    And the recorded traffic for that request has decision "proxied"

  Scenario: A body-JSONPath-conditioned mock only fires on matching body content
    Given Lyrebird boots
    And an upstream "example.local" configured in partition "default" pointing at a fake upstream
    And a mock named "premium" matching POST path "/order" with body path "tier" equals "gold" that responds 200 with body "premium order"
    When I send a POST request to "/order" on the data plane with host "example.local" and JSON body '{"tier":"gold"}'
    Then the response status is 200
    And the response body is "premium order"
    When I send a POST request to "/order" on the data plane with host "example.local" and JSON body '{"tier":"basic"}'
    Then the recorded traffic for that request has decision "proxied"

  Scenario: Validation expressed as an ordinary matching + response rule
    Given Lyrebird boots
    And an upstream "example.local" configured in partition "default" pointing at a fake upstream
    And a mock named "reject-empty-name" matching POST path "/signup" with body path "name" equals "" that responds 422 with body "name is required"
    When I send a POST request to "/signup" on the data plane with host "example.local" and JSON body '{"name":""}'
    Then the response status is 422
    And the response body is "name is required"

  Scenario: A non-matching mock leaves the request to spy passthrough
    Given Lyrebird boots
    And an upstream "example.local" configured in partition "default" pointing at a fake upstream
    And a mock named "other" matching GET path "/other" that responds 200 with body "unused"
    And a fake upstream server that responds 200 with body "from upstream" and header "X-Test: 1"
    And an upstream "passthrough.local" configured in partition "default" pointing at the fake upstream
    When I send a GET request to "/anything" on the data plane with host "passthrough.local"
    Then the response status is 200
    And the response body is "from upstream"
    And the recorded traffic for that request has decision "proxied"

  Scenario: A seeded mock cannot be updated or deleted via the control plane
    Given a seed file declares a mock named "locked" in partition "default" matching GET path "/locked" that responds 200 with body "locked-response"
    And Lyrebird boots
    When I attempt to update the mock "locked" in partition "default" via the control plane
    Then the control plane responds with status 409
    When I attempt to delete the mock "locked" in partition "default" via the control plane
    Then the control plane responds with status 409

  Scenario: Match test reports which mock would fire without sending anything onward
    Given Lyrebird boots
    And a mock named "ping" matching GET path "/ping" that responds 200 with body "pong"
    When I request a match test for GET path "/ping"
    Then the match-test winner is "ping" with status 200 and body "pong"
    When I request a match test for GET path "/other"
    Then the match-test has no winner
