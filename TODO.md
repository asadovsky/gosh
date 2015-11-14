- Add single-binary mechanism, by means of a function registry
- In cmd.start(), wrap every child process with a "supervisor" process that
  calls WatchParent
