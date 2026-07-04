Feature: Scripting
  As an agent
  I want to attach a sandboxed script to a mock that branches on request data
  So that I can express dynamic behavior beyond static declarative rules (FR-014/015/016, SC-010)

  Background:
    Given a fresh temporary Lyrebird data directory
    And Lyrebird boots
    And an upstream "example.local" configured in partition "default" pointing at a fake upstream

  Scenario Outline: A respond_src script branches on a body field
    Given a mock named "echo" matching POST path "/echo" with script.respond_src "({echoed: req.body.field})" that responds 200
    When I send a POST request to "/echo" on the data plane with host "example.local" and JSON body '{"field":"<value>"}'
    Then the response status is 200
    And the response body is '{"echoed":"<value>"}'

    Examples:
      | value |
      | alpha |
      | beta  |

  Scenario: The sandbox exposes no filesystem, network, or environment globals
    Given a mock named "sandboxed" matching GET path "/sandboxed" with script.respond_src "({fetch: typeof fetch, proc: typeof process, req: typeof require})" that responds 200
    When I send a GET request to "/sandboxed" on the data plane with host "example.local"
    Then the response status is 200
    And the response body is '{"fetch":"undefined","proc":"undefined","req":"undefined"}'

  Scenario: An infinite-loop script fails safe, is recorded, and the server keeps serving
    Given a mock named "loop" matching GET path "/loop" with script.respond_src "while(true){}" that responds 200
    And a mock named "ping" matching GET path "/ping" that responds 200 with body "pong"
    When I send a GET request to "/loop" on the data plane with host "example.local"
    Then the response status is 500
    And the recorded traffic for that request has decision "script_failed"
    When I send a GET request to "/ping" on the data plane with host "example.local"
    Then the response status is 200
    And the response body is "pong"
