Feature: Multi-tenant isolation via spaces
  As an agent running many concurrent test sessions
  I want each space (partition) to fully isolate its mocks, traffic, and upstreams from every other space
  So that unrelated agents never see or clobber each other's state (FR-023/024, SC-004)

  Background:
    Given a fresh temporary Lyrebird data directory
    And Lyrebird boots

  Scenario: Contradictory mocks on the same route resolve independently per space
    Given a mock named "route" in space "space-a" matching GET path "/r" that responds 500 with body "err-a"
    And a mock named "route" in space "space-b" matching GET path "/r" that responds 200 with body "ok-b"
    When I send a GET request to "/r" on the data plane with host "example.local" in partition "space-a"
    Then the response status is 500
    And the response body is "err-a"
    When I send a GET request to "/r" on the data plane with host "example.local" in partition "space-b"
    Then the response status is 200
    And the response body is "ok-b"

  Scenario: A space's traffic never leaks into another space's listing
    Given a mock named "route" in space "space-a" matching GET path "/r" that responds 200 with body "ok"
    When I send a GET request to "/r" on the data plane with host "example.local" in partition "space-a"
    Then traffic is recorded in space "space-a"
    And no traffic is recorded in space "space-b"

  Scenario: Deleting a space cascades its mocks, traffic, and upstreams
    Given a mock named "route" in space "space-a" matching GET path "/r" that responds 200 with body "ok"
    And an upstream "example.local" configured in partition "space-a" pointing at a fake upstream
    When I send a GET request to "/r" on the data plane with host "example.local" in partition "space-a"
    Then the response status is 200
    When I delete the space "space-a" via the control plane
    Then the space control plane responds with status 204
    And space "space-a" has no mocks, traffic, or upstreams left

  Scenario: The default space cannot be deleted
    When I attempt to delete the space "default" via the control plane
    Then the space control plane responds with status 400

  Scenario: The default space is always listed without explicit registration
    When I request the list of spaces via the control plane
    Then the space list includes "default"

  Scenario: A space registered via the control plane appears in the space list
    Given I create a space "registered-a" with description "created via REST" via the control plane
    Then the space control plane responds with status 200
    When I request the list of spaces via the control plane
    Then the space list includes "registered-a"

  Scenario: create_space, list_spaces, and delete_space are exposed identically over MCP
    Given an MCP client is connected to the control plane
    When I call the MCP tool "create_space" with arguments '{"id":"mcp-space","description":"created via MCP"}'
    Then the MCP call succeeds
    And the MCP structured result field "id" equals "mcp-space"
    When I call the MCP tool "list_spaces" with arguments '{}'
    Then the MCP call succeeds
    When I call the MCP tool "delete_space" with arguments '{"id":"mcp-space"}'
    Then the MCP call succeeds
    When I call the MCP tool "delete_space" with arguments '{"id":"default"}'
    Then the MCP call fails with an explanatory error
    And the MCP result text contains "default partition cannot be deleted"
