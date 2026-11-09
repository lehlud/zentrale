import sys

sys.dont_write_bytecode = True
from compute import triangle

for index in range(60000):
    print("build progress line %06d: checking input" % index)
assert triangle(10) == 55, "final check: triangle(10) must be 55"
print("PASS")
