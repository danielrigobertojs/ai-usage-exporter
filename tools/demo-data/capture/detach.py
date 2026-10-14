#!/usr/bin/env python3
"""Launch a command in its own session so it survives the parent's process-group
kill. macOS has no setsid(1); os.setsid() does the same thing from Python."""
import os
import sys

pid = os.fork()
if pid > 0:
    print(pid)
    sys.exit(0)

os.setsid()
log = os.environ.get("DETACH_LOG", "/dev/null")
fd = os.open(log, os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o644)
os.dup2(fd, 1)
os.dup2(fd, 2)
os.dup2(os.open(os.devnull, os.O_RDONLY), 0)
os.execvp(sys.argv[1], sys.argv[1:])