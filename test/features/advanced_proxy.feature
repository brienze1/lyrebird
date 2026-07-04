Feature: Advanced proxy & fault injection
  As an agent testing resilience and edge cases
  I want to rewrite/transform proxied traffic, inject faults, restrict which hosts may be proxied, and script sequential mock responses
  So that I can exercise scenarios a plain spy/mock can't (FR-004/005/006, SC-010)

  Background:
    Given a fresh temporary Lyrebird data directory

  Scenario: A rewrite_request script changes what the upstream receives
    Given Lyrebird boots
    And an upstream "example.local" configured in partition "default" pointing at a fake upstream
    And a proxy mock named "rewriter" matching GET path "/r" with rewrite_request script "({path: \"/rewritten\", headers: {\"X-Injected\": \"yes\"}})"
    When I send a GET request to "/r" on the data plane with host "example.local"
    Then the response status is 200
    And the fake upstream received path "/rewritten"
    And the fake upstream received header "X-Injected" equals "yes"

  Scenario: A transform_response script changes what the client receives
    Given Lyrebird boots
    And a fake upstream server that responds 200 with body "real" and header ""
    And an upstream "example.local" configured in partition "default" pointing at the fake upstream
    And a proxy mock named "transformer" matching GET path "/t" with transform_response script "({body: JSON.stringify({wrapped: resp.body})})"
    When I send a GET request to "/t" on the data plane with host "example.local"
    Then the response status is 200
    And the response body is '{"wrapped":"real"}'

  Scenario: A broken rewrite script forwards the real request unmodified and the server keeps serving
    Given Lyrebird boots
    And an upstream "example.local" configured in partition "default" pointing at a fake upstream
    And a proxy mock named "broken" matching GET path "/broken" with rewrite_request script "throw new Error('boom')"
    And a mock named "ping" matching GET path "/ping" that responds 200 with body "pong"
    When I send a GET request to "/broken" on the data plane with host "example.local"
    Then the response status is 200
    And the recorded traffic for that request has decision "proxied"
    And the fake upstream received path "/broken"
    When I send a GET request to "/ping" on the data plane with host "example.local"
    Then the response status is 200
    And the response body is "pong"

  Scenario: A delay fault injects observable latency before responding
    Given Lyrebird boots
    And a mock named "slow" matching GET path "/slow" with fault delay 300ms
    When I send a timed GET request to "/slow" on the data plane with host "example.local"
    Then the timed request succeeded with status 200
    And the request took at least "250ms"

  Scenario: A malformed fault writes garbage bytes instead of a valid HTTP response
    Given Lyrebird boots
    And a mock named "garbage" matching GET path "/garbage" with fault kind "malformed"
    When I send a GET request to "/garbage" on the data plane with host "example.local" with a client timeout of "2s"
    Then the request fails

  Scenario: A reset fault closes the connection immediately
    Given Lyrebird boots
    And a mock named "abrupt" matching GET path "/abrupt" with fault kind "reset"
    When I send a GET request to "/abrupt" on the data plane with host "example.local" with a client timeout of "2s"
    Then the request fails within "500ms"

  Scenario: A timeout fault never responds until the client gives up
    Given Lyrebird boots
    And a mock named "hangs" matching GET path "/hangs" with fault kind "timeout"
    When I send a GET request to "/hangs" on the data plane with host "example.local" with a client timeout of "300ms"
    Then the request times out without any response

  Scenario: A non-allowed host is blocked and recorded, while an allowed host still proxies
    Given the allowed proxy hosts are configured to "example.local"
    And Lyrebird boots
    And an upstream "example.local" configured in partition "default" pointing at a fake upstream
    And an upstream "blocked.local" configured in partition "default" pointing at a fake upstream
    When I send a GET request to "/anything" on the data plane with host "blocked.local"
    Then the response status is 403
    And the recorded traffic for that request has decision "blocked"
    When I send a GET request to "/anything" on the data plane with host "example.local"
    Then the response status is 200

  Scenario: A repeat_last scenario mock serves its last response forever once exhausted
    Given Lyrebird boots
    And a scenario mock named "seq-repeat" matching GET path "/seq" with responses "one,two" and on_exhaust "repeat_last"
    When I send a GET request to "/seq" on the data plane with host "example.local"
    Then the response body is "one"
    When I send a GET request to "/seq" on the data plane with host "example.local"
    Then the response body is "two"
    When I send a GET request to "/seq" on the data plane with host "example.local"
    Then the response body is "two"

  Scenario: A wrap scenario mock cycles back to its first response
    Given Lyrebird boots
    And a scenario mock named "seq-wrap" matching GET path "/seq" with responses "one,two" and on_exhaust "wrap"
    When I send a GET request to "/seq" on the data plane with host "example.local"
    Then the response body is "one"
    When I send a GET request to "/seq" on the data plane with host "example.local"
    Then the response body is "two"
    When I send a GET request to "/seq" on the data plane with host "example.local"
    Then the response body is "one"

  Scenario: A fallthrough scenario mock stops matching once exhausted
    Given Lyrebird boots
    And a scenario mock named "seq-fallthrough" matching GET path "/seq" with responses "one" and on_exhaust "fallthrough"
    When I send a GET request to "/seq" on the data plane with host "example.local"
    Then the response body is "one"
    When I send a GET request to "/seq" on the data plane with host "example.local"
    Then the response status is 404
    And the response body contains "not_configured"
