#[cfg(feature = "hello-stdout")]
mod hello_stdout;

#[cfg(feature = "runtime-env")]
mod runtime_env;

#[cfg(feature = "github-api-get")]
mod github_api_get;

pub fn run(println: &dyn Fn(&dyn std::fmt::Display) -> ()) {
    let features: &[&dyn Fn()] = &[
        #[cfg(feature = "hello-stdout")]
        &hello_stdout::hello_stdout,
        #[cfg(feature = "runtime-env")]
        &|| runtime_env::inspect(println),
        #[cfg(feature = "github-api-get")]
        &|| github_api_get::print(println),
    ];

    for feature in features {
        feature();
        println(&"");
    }

    println(&"YOLO!");
}
