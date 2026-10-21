-- Sign-in looks an account up by its lower-cased address, but the first
-- administrator was stored exactly as ARGUS_ADMIN_EMAIL was typed. One typed
-- with a capital letter could never sign in. Store those the way they are
-- looked up.
--
-- An address is left alone when any other account has the same address in
-- another case. The count is taken over every row, changed or not: checking
-- only for an account already in lower case missed two accounts that both
-- needed changing ("John@" and "jOhn@"), which then collided on the unique
-- index, failed the migration, and stopped Argus from starting.
UPDATE users u SET email = lower(u.email)
 WHERE u.email <> lower(u.email)
   AND (SELECT count(*) FROM users o WHERE lower(o.email) = lower(u.email)) = 1;
