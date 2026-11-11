# upsert-comment-own-author - tasks

- [x] 1. Marker match also requires the comment's author to be the token's account
- [x] 2. Foreign marker comments are left alone and logged with their author; reeve creates its own
- [x] 3. Unresolvable identity fails the upsert closed with no edits or creates
- [x] 4. Tests: foreign-alone; own-only edited; identity failure fails closed
