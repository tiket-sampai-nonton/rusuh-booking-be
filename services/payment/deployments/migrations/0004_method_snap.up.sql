ALTER TABLE payment_attempts DROP CONSTRAINT IF EXISTS payment_attempts_method_check;
ALTER TABLE payment_attempts ADD CONSTRAINT payment_attempts_method_check
    CHECK (method IN ('VA_BCA','VA_BNI','VA_BRI','VA_PERMATA','QRIS','GOPAY','SNAP'));
