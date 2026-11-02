@edit
Feature: Edit order (TC-EDT)

  Scenario: TC-EDT-01 change items before paying
    Given a seeded edit event with 10 seats
    And an edit visitor holds 1 ticket
    When the edit visitor replaces items with a different NIK
    Then the edit succeeds with status 200
    And the booking version is greater than 1
    And the payment TTL was recorded before the edit

  Scenario: TC-EDT-03 edit limit reached
    Given a seeded edit event with 10 seats and max_edits=1
    And an edit visitor holds 1 ticket
    When the edit visitor edits 2 times
    Then the 2nd edit returns status 409
    And the edit error code is "EDIT_LIMIT_REACHED"

  Scenario: TC-EDT-04 edit locked after payment-lock
    Given a seeded edit event with 5 seats
    And an edit visitor holds 1 ticket
    And a payment-lock is applied on the edit booking
    When the edit visitor tries to edit the locked booking
    Then the edit response status is 409
    And the edit error code is "BOOKING_LOCKED"

  Scenario: TC-EDT-06 concurrent edits with same version — exactly one wins
    Given a seeded edit event with 10 seats
    And an edit visitor holds 1 ticket
    When two concurrent edits are sent with the same version
    Then exactly one edit returns 200 and one returns 409
