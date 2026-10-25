#!/usr/bin/env python3
"""Explicitly replace the seven canonical draft artifacts from their feature source."""
from contract_sources import ROOT,NAMES,expected_content

if __name__=='__main__':
    for name in NAMES:(ROOT/'contracts'/name).write_bytes(expected_content(name))
    print('Promoted draft contracts; wire files are unchanged and the index data-model link is rebased. Review status is unchanged.')
