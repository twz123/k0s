pub(crate) fn hello_stdout() {
    let stdout = wasi::cli::stdout::get_stdout();
    stdout
        .blocking_write_and_flush(b"Hello, wasi:cli/stdout!\n")
        .unwrap();
    println!("Hello, Rust stdlib stdout!");
}
