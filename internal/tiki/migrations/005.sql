-- Version 5: one optional external link per item, such as its pull request.
ALTER TABLE items ADD COLUMN url TEXT NOT NULL DEFAULT '';
