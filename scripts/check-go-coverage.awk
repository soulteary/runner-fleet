# Check exact covered statement counts, without rounding percentages or counting
# duplicate profile blocks twice. All production Go packages stay in the profile.
NR == 1 {
    if ($0 !~ /^mode: (set|count|atomic)$/) {
        print "invalid coverage profile header" > "/dev/stderr"
        invalid = 1
        exit 2
    }
    next
}
NF != 3 || $2 !~ /^[0-9]+$/ || $3 !~ /^[0-9]+$/ {
    print "invalid coverage profile record" > "/dev/stderr"
    invalid = 1
    exit 2
}
{
    if (!($1 in statements)) {
        statements[$1] = $2
        total += $2
    } else if (statements[$1] != $2) {
        print "inconsistent coverage block" > "/dev/stderr"
        invalid = 1
        exit 2
    }
    if ($3 > 0 && !covered[$1]) {
        covered[$1] = 1
        hit += $2
    }
}
END {
    if (invalid) exit 2
    if (total <= 0) {
        print "coverage profile contains no production statements" > "/dev/stderr"
        exit 2
    }
    printf "coverage: %.4f%% (%d/%d production statements); required: 95%%\n", 100 * hit / total, hit, total
    if (100 * hit < 95 * total) exit 1
}
