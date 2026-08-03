# Trash

Deleting a file moves it to the trash rather than removing it. Each trashed
entry remembers where it came from, when it was deleted and by whom.

## Restoring and purging

From the **Trash** screen you can **restore** an entry to its original location,
or **purge** it to delete it permanently. Emptying the trash removes everything
in it.

Trash is also purged automatically, alongside abandoned upload fragments, when
the disk approaches its [reserve threshold](/reference/environment#zefile-reserve-bytes)
— reclaiming space is preferred to letting the service go read-only.
