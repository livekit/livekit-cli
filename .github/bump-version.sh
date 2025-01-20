#!/usr/bin/env bash

sed -i "s/^\\tVersion.*\$/\tVersion = \"$VERSION\"/" version.go
