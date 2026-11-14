-- +goose Up
CREATE INDEX rhythm_diaries_updated_at ON rhythm_diaries (updated_at);
