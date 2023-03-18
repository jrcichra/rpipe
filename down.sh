#!/bin/sh
sudo iptables -A INPUT -p tcp --destination-port 8000 -j DROP
