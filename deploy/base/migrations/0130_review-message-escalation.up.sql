-- WL-REQ-1774: an escalation reply on a question is the author's, but it is
-- not the answer, so the first-answer check skips it.
ALTER TABLE review_messages ADD COLUMN escalation boolean NOT NULL DEFAULT false;
