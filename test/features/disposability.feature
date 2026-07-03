Feature: Disposability of persisted state
  As an operator running Lyrebird
  I want the server to boot healthy no matter what state its SQLite file is in
  So that lost or unreadable data is never treated as corruption (Principle III, FR-029)

  Background:
    Given a fresh temporary Lyrebird data directory

  Scenario: Boot with no database file at all
    Given no database file exists at the configured path
    When Lyrebird boots
    Then boot succeeds
    And the control plane reports ready

  Scenario: Boot with a corrupted database file
    Given a corrupted (non-SQLite) file exists at the configured path
    When Lyrebird boots
    Then boot succeeds
    And the control plane reports ready

  Scenario: Boot with a database written under a different at-rest key
    Given a database at the configured path contains an ephemeral mock "stale-mock" in partition "default" encrypted with data key "keyA"
    When Lyrebird boots with data key "keyB"
    Then boot succeeds
    And the control plane reports ready
    And listing ephemeral mocks for partition "default" returns zero results

  Scenario: Seeded mocks still load when ephemeral state is unreadable
    Given a database at the configured path contains an ephemeral mock "stale-mock" in partition "default" encrypted with data key "keyA"
    And a seed file declares a mock named "always-on" in partition "default"
    When Lyrebird boots with data key "keyB"
    Then boot succeeds
    And the seeded mock "always-on" is present in partition "default"
