@queue
Feature: Queue and admission guarantee (TC-QUE, TC-ADM)

  Scenario: TC-QUE-02 direct admission when slots available
    Given a seeded queue event with 5 seats
    When a queue visitor joins
    Then the visitor is ADMITTED or moves to ADMITTED within 10 seconds
    And an admission token is returned after claiming

  Scenario: TC-ADM-01 admission always reserves stock
    Given a seeded queue event with 3 seats
    When a queue visitor is admitted and claims
    Then allocated_units is at least 1

  Scenario: TC-ADM-03 unused units return to supply after hold
    Given a seeded queue event with 5 seats
    And a queue visitor is admitted with 3 reserved units and creates a session
    When the admitted visitor holds only 1 ticket
    Then the hold succeeds
    And allocated_units decreases by at least 2 after hold
