@booking
Feature: Booking and session invariants (TC-BKG, TC-SES)

  Scenario: TC-BKG-01 buyer can hold up to 3 tickets with different attendee NIKs
    Given a seeded booking event with 10 seats
    And a booking visitor is admitted
    When the booking visitor holds 3 tickets with different attendee NIKs
    Then the hold succeeds with status 201
    And supply shows held_units equal to 3

  Scenario: TC-BKG-02 attendee NIK must be unique per event
    Given a seeded booking event with 10 seats
    And booking visitor A holds a ticket with attendee NIK "3174010101800001"
    And a second booking visitor is admitted
    When booking visitor B tries to hold with the same attendee NIK "3174010101800001"
    Then the booking response status is 409
    And the booking error code is "ATTENDEE_NIK_ALREADY_REGISTERED"

  Scenario: TC-BKG-03 duplicate attendee NIK in same order is rejected
    Given a seeded booking event with 10 seats
    And a booking visitor is admitted
    When the booking visitor holds 2 tickets with the same attendee NIK "3174010101800001"
    Then the booking response status is 409
    And the booking error code is "ATTENDEE_NIK_DUPLICATE_IN_ORDER"

  Scenario: TC-BKG-06 hold is idempotent with same Idempotency-Key
    Given a seeded booking event with 5 seats
    And a booking visitor is admitted
    When the booking visitor holds 1 ticket with key "idem-key-bkg-001"
    Then the hold succeeds with status 201
    When the same hold is resent with key "idem-key-bkg-001"
    Then the booking response status is 201
    And the booking ID is the same as before

  Scenario: TC-BKG-08 hold with invalid NIK structure is rejected
    Given a seeded booking event with 5 seats
    And a booking visitor is admitted
    When the booking visitor tries to hold with invalid NIK "1234"
    Then the booking response status is 422
    And the booking error code is "NIK_INVALID"

  Scenario: TC-BKG-09 attendee check does not leak NIK
    Given a seeded booking event with 5 seats
    And a booking visitor is admitted
    When checking attendee NIK "3174030303800003" in the session
    Then the attendee check response status is 200
    And the attendee check result is "AVAILABLE"
    And the check response does not contain the NIK "3174030303800003"

  Scenario: TC-SES-01 booking response has server-authoritative paymentExpiresAt
    Given a seeded booking event with 5 seats
    And a booking visitor is admitted
    When the booking visitor holds 1 ticket
    Then the booking response contains paymentExpiresAt
    And paymentExpiresAt is in the future
