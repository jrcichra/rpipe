#!/bin/sh
sudo iptables -D INPUT -p tcp --destination-port 8000 -j DROP 