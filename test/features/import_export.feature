Feature: Import/export (seed round-trip)
  As an agent building up a session's worth of mocks/upstreams via the API
  I want to snapshot that runtime state out as YAML and restore it later
  So that I can reuse it as a mounted seed file or move it between environments (T061)

  Background:
    Given a fresh temporary Lyrebird data directory

  Scenario: Export includes ephemeral mocks and upstreams but not seeded ones
    Given a seed file declares a mock named "seeded-ping" in partition "default"
    And Lyrebird boots
    And a mock named "ephemeral-ping" matching GET path "/ping" that responds 200 with body "pong"
    And an upstream "example.local" configured in partition "default" pointing at a fake upstream
    When I export the space "default" over REST
    Then the export response status is 200
    And the exported bundle includes a mock named "ephemeral-ping"
    And the exported bundle does not include a mock named "seeded-ping"
    And the exported bundle includes an upstream for host "example.local"

  Scenario: Export is identical over REST and MCP
    Given Lyrebird boots
    And a mock named "ephemeral-ping" matching GET path "/ping" that responds 200 with body "pong"
    When I export the space "default" over REST
    And an MCP client is connected to the control plane
    And I call the MCP tool "export_config" with arguments '{}'
    Then the MCP call succeeds
    And the MCP result text contains "ephemeral-ping"

  Scenario: Round-trip fidelity: export, reset, import, the mock still fires
    Given Lyrebird boots
    And an upstream "example.local" configured in partition "default" pointing at a fake upstream
    And a mock named "ephemeral-ping" matching GET path "/ping" that responds 200 with body "pong"
    When I export the space "default" over REST
    And I reset the space "default"
    And I import the last exported bundle into space "default"
    Then the import response status is 200
    When I send a GET request to "/ping" on the data plane with host "example.local"
    Then the response status is 200
    And the response body is "pong"

  Scenario: Import is additive, not destructive
    Given Lyrebird boots
    And an upstream "example.local" configured in partition "default" pointing at a fake upstream
    And a mock named "first" matching GET path "/first" that responds 200 with body "one"
    When I export the space "default" over REST
    And a mock named "second" matching GET path "/second" that responds 200 with body "two"
    And I import the last exported bundle into space "default"
    Then the import response status is 200
    When I send a GET request to "/second" on the data plane with host "example.local"
    Then the response status is 200
    And the response body is "two"

  Scenario: Export is scoped to its own partition
    Given Lyrebird boots
    And a mock named "space-a-mock" in space "space-a" matching GET path "/a" that responds 200 with body "a"
    And a mock named "space-b-mock" in space "space-b" matching GET path "/b" that responds 200 with body "b"
    When I export the space "space-a" over REST
    Then the exported bundle includes a mock named "space-a-mock"
    And the exported bundle does not include a mock named "space-b-mock"

  Scenario: A malformed import body fails with an explanatory error
    Given Lyrebird boots
    When I import the following malformed body into space "default":
      """
      not: [valid, yaml, bundle structure :::
      """
    Then the import response status is 400
