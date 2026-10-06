# Worked example: "the last collection looks wrong"

Illustrative: the shape of a good run, with results shortened and every name made up.

**Question:** "The newest snapshot is missing 12 devices. Fix it."

1. `inspect-snapshots` -> newest `5140` is PROCESSED but incomplete (12 devices missing); age 2 hours.
2. `inspect-collection` `view: status` -> last task finished; collector connected; 12 failed devices.
3. `investigate-collection-failure` -> all 12 are `credentials` failures on the same jump server login. A device-side cause: it is fixed outside Forward.
4. Repair choice: reprocessing would not help (the data was never collected) and collecting again fails the same way. Report the cause and what the person must fix; offer `edit-collection` after the credential is fixed.
5. After they fix it: `edit-collection` dry run, approved, applied; `inspect-snapshots` shows `5141` PROCESSED and complete.

**Answer:** the cause with its evidence, the repair chosen and why, and what was not checked. "Started" is not "fixed".
