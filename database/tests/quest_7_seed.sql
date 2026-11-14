-- Synthetic data for the disposable Quest 7 cluster.
INSERT INTO rhythm_identities (id)
SELECT kind || '_' || repeat(suffix, 64)
FROM (VALUES ('g', 'a'), ('u', 'a'), ('g', 'b'), ('u', 'b')) AS ids(kind, suffix);

INSERT INTO rhythm_diaries (diary_key, owner, revision, document, updated_at)
SELECT 'g_' || repeat(suffix, 64), 'u_' || repeat(suffix, 64), revision,
       jsonb_build_object('revision', revision, 'profile', NULL, 'entries',
         jsonb_build_array(jsonb_build_object('id', entry, 'status', 'wellbeing',
                          'date', '2026-11-14', 'sleep', sleep, 'fatigue', fatigue))),
       '2026-11-14 09:00:00+00'::timestamptz
FROM (VALUES ('a', 3, 'quest-sleep-a', 6.5, 4), ('b', 7, 'quest-sleep-b', 5.5, 9))
     AS fixture(suffix, revision, entry, sleep, fatigue);

INSERT INTO rhythm_accounts
    (principal, login, password_salt, password_hash, password_iterations, recovery_digest, created_at)
SELECT 'u_' || repeat(suffix, 64), 'quest7.' || suffix, 'fixture-salt', 'fixture-hash',
       600000, repeat(suffix, 64), '2026-11-14 09:00:00+00'::timestamptz
FROM (VALUES ('a'), ('b')) AS fixture(suffix);
