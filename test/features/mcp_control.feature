Feature: MCP control plane
  As an agent
  I want to manage Lyrebird entirely over MCP
  So that MCP is a genuine primary control plane, not a REST afterthought (FR-018/019/020, SC-002, SC-005)

  Background:
    Given a fresh temporary Lyrebird data directory
    And Lyrebird boots
    And an MCP client is connected to the control plane

  Scenario: The guide tool returns a usable example
    When I call the MCP tool "lyrebird_guide" with arguments '{}'
    Then the MCP call succeeds
    And the MCP result text contains "create_mock"

  Scenario: Create, match-test, and fire a mock in a handful of MCP calls
    Given an upstream "example.local" configured in partition "default" pointing at a fake upstream
    When I call the MCP tool "create_mock" with arguments '{"name":"ping","match":{"method":"GET","path":"/ping"},"action":{"respond":{"status":200,"body":"pong"}}}'
    Then the MCP call succeeds
    When I call the MCP tool "match_test" with arguments '{"sample_request":{"method":"GET","path":"/ping"}}'
    Then the MCP call succeeds
    And the MCP structured result field "winner.name" equals "ping"
    When I send a GET request to "/ping" on the data plane with host "example.local"
    Then the response status is 200
    And the response body is "pong"

  Scenario: Promote a recorded interaction into a mock with full fidelity
    Given a fake upstream server that responds 200 with body "real upstream body" and header "X-Test: 1"
    And an upstream "example.local" configured in partition "default" pointing at the fake upstream
    When I send a GET request to "/anything" on the data plane with host "example.local"
    Then the response status is 200
    When I promote the last recorded traffic into a mock named "promoted"
    Then the MCP call succeeds
    When I send a GET request to "/anything" on the data plane with host "unmapped.local"
    Then the response status is 200
    And the response body is "real upstream body"
    And the response header "X-Test" is "1"

  Scenario: An invalid request returns an explanatory error, not a raw failure
    When I call the MCP tool "create_mock" with arguments '{"name":"bad","match":{"path":"~("},"action":{"respond":{"status":200,"body":"x"}}}'
    Then the MCP call fails with an explanatory error

  Scenario: A mock cannot be created with a seeded lifetime via MCP
    When I call the MCP tool "create_mock" with arguments '{"name":"sneaky","lifetime":"seeded","match":{"method":"GET","path":"/x"},"action":{"respond":{"status":200,"body":"x"}}}'
    Then the MCP call fails with an explanatory error
