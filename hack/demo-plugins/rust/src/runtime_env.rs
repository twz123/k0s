use std::{
    env,
    fmt::{self, Write},
    fs, path, time,
};

pub(super) fn inspect(println: &dyn Fn(&dyn std::fmt::Display) -> ()) {
    println(&"Arguments:");
    for arg in env::args_os() {
        println(&format_args!("{arg:?}"));
    }

    println(&"\nEnvironment Variables:");
    for (key, value) in env::vars_os() {
        println(&format_args!("{key:?}={value:?}"));
    }

    println(&"\nFile System:");
    visit_dirs(path::Path::new("/"), &println)
}

fn visit_dirs(dir: &path::Path, println: &dyn Fn(&dyn std::fmt::Display) -> ()) {
    let entries = match fs::read_dir(dir) {
        Ok(entries) => entries,
        Err(e) => {
            println(&format_args!("!\tError reading dir: {}", e));
            return;
        }
    };

    for entry in entries {
        let entry = match entry {
            Ok(entry) => entry,
            Err(e) => {
                println(&format_args!("!\tError reading entry: {}", e));
                continue;
            }
        };

        let meta = match entry.metadata() {
            Ok(meta) => meta,
            Err(e) => {
                println(&format_args!(
                    "!\tCouldn't get metadata for {:?}: {}",
                    entry.path(),
                    e
                ));
                continue;
            }
        };

        let path = entry.path();
        println(&format_args!(
            "{}\t{}\n",
            &FsMetaDisplay(&meta),
            path.display()
        ));

        if meta.is_dir() {
            visit_dirs(&entry.path(), println)
        }
    }
}

struct FsMetaDisplay<'a>(&'a fs::Metadata);

impl<'a> fmt::Display for FsMetaDisplay<'a> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let meta = self.0;

        f.write_char(match meta.file_type() {
            ft if ft.is_dir() => 'd',
            ft if ft.is_symlink() => 'l',
            _ => 'f',
        })?;

        f.write_fmt(format_args!("\t{}b", meta.len()))?;

        match meta.modified() {
            Ok(modified) => match modified.duration_since(time::UNIX_EPOCH) {
                Ok(after) => f.write_fmt(format_args!("\t{after:?}")),
                Err(earlier) => {
                    let earlier = earlier.duration();
                    f.write_fmt(format_args!("\t-{earlier:?}"))
                }
            },
            Err(e) => f.write_fmt(format_args!("\t??? {e}")),
        }
    }
}
