# Icon transform goldens

`symbolic/` holds SVGs and what the symbolic transform makes of each.
The goldens were captured from lys-shell's original Rust transform (on usvg
0.46), which the Go port reproduces byte for byte, and are now the
reference:

- `<name>.svg.golden`: `to_symbolic`'s output, or `NONE` when it gives
  up;
- `<name>.svg.parse`: `OK`, or `ERR: ` and the error the parser rejects
  the file with.

`TestToSymbolicMatchesRust` holds the transform to both.

The inputs are a sample of the four icon sets lys-shell installs from
(Simple Icons `si-`, CC0; Lucide `ld-`, ISC; Tabler outline `tb-` and
filled `tbf-`, MIT; Material Symbols outlined `md-`, Apache-2.0), of
Adwaita's symbolic icons (`adwaita-`, CC-BY-SA-3.0/LGPL-3.0), and
hand-written `edge-` cases for what the sets rarely exercise: broken
XML with and without a recoverable `d`, CSS, `use`/`symbol`, nested
`svg`, every join and cap, dashes, units, clips, masks, filters,
markers (orientations, units, view boxes, paint order), and so on;
and `edge-xml-` cases for the XML layer (roxmltree): every error it
reports, entities (markup, loops, attribute normalization),
namespaces, and the text `<style>` is read from.

A change to the transform's output is a behavior change: review the
diff of the affected goldens and update them in the same commit.
