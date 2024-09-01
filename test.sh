#!/bin/bash

echo "This script will install a package. Do you want to continue? (y/n)"
read -r response
if [[ "$response" == "y" ]]; then
    echo instaling
else
    echo "Installation aborted."
fi