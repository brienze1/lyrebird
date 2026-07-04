Feature: Lifetimes & bounded storage
  As an agent running long-lived or repeated sessions
  I want seeded fixtures to survive reset, ephemeral mocks to expire on their own, and old traffic to be swept automatically
  So that storage stays bounded without me having to manage cleanup myself (FR-025/026/027/028, SC-006)

  Background:
    Given a fresh temporary Lyrebird data directory
    And the GC interval is configured to "100ms"

  Scenario: An ephemeral mock's TTL expires and the GC loop removes it
    Given Lyrebird boots
    And an ephemeral mock named "temp" matching GET path "/temp" that responds 200 with body "temp" and ttl_seconds 1
    When I send a GET request to "/temp" on the data plane with host "example.local"
    Then the response status is 200
    And the response body is "temp"
    When I wait "1500ms"
    And I send a GET request to "/temp" on the data plane with host "example.local"
    Then the response status is 404
    And the response body contains "not_configured"

  Scenario: Reset preserves seeded mocks but removes ephemeral ones
    Given a seed file declares a mock named "kept" in partition "default" matching GET path "/kept" that responds 200 with body "kept"
    And Lyrebird boots
    And a mock named "temp" matching GET path "/temp" that responds 200 with body "temp"
    When I reset the space "default"
    And I send a GET request to "/kept" on the data plane with host "example.local"
    Then the response status is 200
    And the response body is "kept"
    When I send a GET request to "/temp" on the data plane with host "example.local"
    Then the response status is 404
    And the response body contains "not_configured"

  Scenario: Reset can also clear recorded traffic when requested
    Given Lyrebird boots
    And a mock named "route" matching GET path "/r" that responds 200 with body "ok"
    When I send a GET request to "/r" on the data plane with host "example.local"
    Then the response status is 200
    When I reset the space "default" and clear traffic
    Then no traffic is recorded in space "default"

  Scenario: Traffic older than the retention window is purged within one GC cycle
    Given the traffic TTL is configured to "200ms"
    And Lyrebird boots
    And traffic older than the retention window was recorded in space "default"
    When I wait "500ms"
    Then no traffic is recorded in space "default"
