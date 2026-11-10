@guest
Feature: Anonymous queue join (TC-GST, ADR-030)

  # Join is anonymous for every buyer: the body is not read. NIK and email are given at hold time (booking.feature).

  Scenario: TC-GST-01 anonymous join returns a visitor token
    Given a seeded guest event
    When a guest visitor joins anonymously
    Then the response status is 200 or 201
    And a visitor token is returned

  Scenario: TC-GST-02 legacy buyer fields in the join body are ignored
    Given a seeded guest event
    When a guest visitor joins with legacy NIK "1234" and email "buyer@example.test"
    Then the join response status is 201
    And a visitor token is returned
    And the response body does not contain the NIK plaintext "1234"

  Scenario: TC-GST-04 same visitor joining again gets the same entry
    Given a seeded guest event
    And visitor A has joined anonymously
    When the same visitor A joins again
    Then the join response status is 200
    And the entry is the same as before
