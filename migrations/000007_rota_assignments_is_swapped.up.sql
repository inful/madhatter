-- Add is_swapped flag to rota_assignments to track assignments that resulted from a HAT day swap.
ALTER TABLE rota_assignments ADD COLUMN is_swapped INTEGER NOT NULL DEFAULT 0;
